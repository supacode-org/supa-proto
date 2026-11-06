package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supaproto/internal/client"
	"supaproto/internal/config"
)

// syncBuffer is a goroutine-safe buffer for a subprocess's output: os/exec
// writes to it from its own goroutine while the test polls String().
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type coverCounter struct {
	opened, closed atomic.Int64
	up, down       atomic.Int64
}

func (c *coverCounter) CoverSessionOpened()     { c.opened.Add(1) }
func (c *coverCounter) CoverSessionClosed()     { c.closed.Add(1) }
func (c *coverCounter) CoverBytes(up, down int) { c.up.Add(int64(up)); c.down.Add(int64(down)) }

// startCoverServer runs a server with the given provider and cover setting,
// returning its address, secret and captured output.
func startCoverServer(t *testing.T, provider string, coverEnabled bool) (address, secret string, output *syncBuffer) {
	t.Helper()
	temp := t.TempDir()
	binary := filepath.Join(temp, "supaproto-server")
	build(t, repoRoot(), binary, "./cmd/supaproto-server")
	secret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))
	address = reserveTCPAddress(t)
	path := writeJSON(t, temp, "server.json", map[string]any{
		"listen": address, "handshake_timeout": "3s", "dial_timeout": "3s", "idle_timeout": "10s",
		"log_size": "0M", "log_file": filepath.Join(temp, "server.log"),
		"obfs":  map[string]any{"provider": provider, "participation": 0.65},
		"cover": map[string]any{"enabled": coverEnabled, "max_rate_kbps_per_user": 8000, "max_sessions_per_user": 32},
		"users": []any{map[string]any{
			"client_id": testClientID, "secret": secret, "status": "enabled",
			"max_connections": 64, "allow_private": true,
		}},
	})
	output = &syncBuffer{}
	cmd := exec.Command(binary, "-config", path)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(cmd) })
	waitListen(t, address, output)
	return address, secret, output
}

func startCoverClient(t *testing.T, address, secret, provider string, rateKbps int) (*client.Cover, *coverCounter) {
	t.Helper()
	cfg := config.Client{
		Server: address, ConnectTimeout: "3s", HandshakeTimeout: "3s", IdleTimeout: "10s",
		ClientID: testClientID, Secret: secret,
	}
	cfg.Obfs.Provider, cfg.Obfs.Participation = provider, 0.65
	cfg.Cover.Enabled, cfg.Cover.RateKbps = true, rateKbps
	cfg.Defaults()
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	counter := &coverCounter{}
	cover := cl.StartCover(context.Background(), client.CoverOptions{
		RateKbps: cfg.Cover.RateKbps, Stats: counter,
		Log: func(_ int, _, message string) { t.Log(message) },
	})
	t.Cleanup(cover.Close)
	return cover, counter
}

// TestCoverTrafficDownlink uses iot-cam (s2c): the server streams steady decoy
// data down and the client receives it.
func TestCoverTrafficDownlink(t *testing.T) {
	if testing.Short() {
		t.Skip("cover end-to-end test is slow")
	}
	address, secret, output := startCoverServer(t, "iot-cam", true)
	cover, counter := startCoverClient(t, address, secret, "iot-cam", 600)

	waitFor(t, 25*time.Second, func() bool {
		return counter.opened.Load() >= 1 && counter.down.Load() > 0
	}, output)
	if counter.down.Load() <= 0 {
		t.Fatalf("iot-cam cover (s2c) received no bytes; server output:\n%s", output)
	}
	if !strings.Contains(output.String(), "cover established") {
		t.Errorf("server did not log a cover session; output:\n%s", output)
	}
	cover.Close()
	if counter.closed.Load() < 1 {
		t.Errorf("cover session was not reported closed")
	}
}

// TestCoverTrafficUplink uses mysql (c2s): the client generates steady decoy
// data up and the server sinks it.
func TestCoverTrafficUplink(t *testing.T) {
	if testing.Short() {
		t.Skip("cover end-to-end test is slow")
	}
	address, secret, output := startCoverServer(t, "mysql", true)
	_, counter := startCoverClient(t, address, secret, "mysql", 600)

	waitFor(t, 25*time.Second, func() bool {
		return counter.opened.Load() >= 1 && counter.up.Load() > 0
	}, output)
	if counter.up.Load() <= 0 {
		t.Fatalf("mysql cover (c2s) sent no bytes; server output:\n%s", output)
	}
}

// TestCoverDeclinedWhenServerDisabled checks that a cover-enabled client stops
// (does not open sessions) when the server has cover disabled, leaving real
// traffic unaffected.
func TestCoverDeclinedWhenServerDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("cover end-to-end test is slow")
	}
	address, secret, output := startCoverServer(t, "iot-cam", false)
	_, counter := startCoverClient(t, address, secret, "iot-cam", 600)

	waitFor(t, 15*time.Second, func() bool {
		return strings.Contains(output.String(), "reason=cover_disabled")
	}, output)
	// Give the client a moment; it must not count any opened cover session.
	time.Sleep(time.Second)
	if counter.opened.Load() != 0 {
		t.Fatalf("client opened a cover session against a disabled server: %d", counter.opened.Load())
	}
}

func waitListen(t *testing.T, address string, output *syncBuffer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not listen on %s:\n%s", address, output)
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool, output *syncBuffer) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s; server output:\n%s", timeout, output)
}
