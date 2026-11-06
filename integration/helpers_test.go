package integration

import (
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"supaproto/internal/config"
)

const testClientID = "integration-user"

type testServer struct {
	address string
	secret  string
	output  *bytes.Buffer
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func testProvider() (string, float64) {
	if provider := os.Getenv("SUPAPROTO_TEST_OBFS"); provider != "" && provider != "none" {
		return provider, 0.65
	}
	return "none", 0
}

// startServer builds and runs supaproto-server with one user that may reach
// private addresses, so tests can target local listeners.
func startServer(t *testing.T) testServer {
	t.Helper()
	temp := t.TempDir()
	binary := filepath.Join(temp, "supaproto-server")
	build(t, repoRoot(), binary, "./cmd/supaproto-server")
	provider, participation := testProvider()
	srv := testServer{
		address: reserveTCPAddress(t),
		secret:  base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32)),
		output:  &bytes.Buffer{},
	}
	path := writeJSON(t, temp, "server.json", map[string]any{
		"listen": srv.address, "handshake_timeout": "3s", "dial_timeout": "3s", "idle_timeout": "10s",
		"log_size": "0M", "log_file": filepath.Join(temp, "server.log"),
		"obfs": map[string]any{"provider": provider, "participation": participation},
		"udp": map[string]any{
			"enabled": true, "idle_timeout": "10s", "max_datagram_size": 61440, "queue_packets": 32,
			"max_associations_per_user": 8, "max_destinations_per_association": 32,
			"packets_per_second_per_user": 1000, "bytes_per_second_per_user": 1048576,
		},
		"users": []any{map[string]any{
			"client_id": testClientID, "secret": srv.secret, "status": "enabled",
			"max_connections": 32, "allow_private": true,
		}},
	})
	cmd := exec.Command(binary, "-config", path)
	cmd.Stdout, cmd.Stderr = srv.output, srv.output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(cmd) })
	waitTCP(t, srv.address, srv.output)
	return srv
}

// clientConfig returns a client configuration for srv.
func (srv testServer) clientConfig() config.Client {
	provider, participation := testProvider()
	cfg := config.Client{
		Server: srv.address, ConnectTimeout: "3s", HandshakeTimeout: "3s", IdleTimeout: "10s",
		ClientID: testClientID, Secret: srv.secret,
	}
	cfg.Obfs.Provider, cfg.Obfs.Participation = provider, participation
	cfg.Defaults()
	return cfg
}

// startTCPEcho echoes everything and half-closes after the peer does.
func startTCPEcho(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.AcceptTCP()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
				_ = conn.CloseWrite()
			}()
		}
	}()
	return l
}
