package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTCPOverSOCKSEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-level TCP integration test")
	}
	srv := startServer(t)
	echo := startTCPEcho(t)

	temp := t.TempDir()
	clientBinary := filepath.Join(temp, "supaproto-client")
	build(t, repoRoot(), clientBinary, "./cmd/supaproto-client")
	cfg := srv.clientConfig()
	socksAddress := reserveTCPAddress(t)
	path := writeJSON(t, temp, "client.json", map[string]any{
		"socks_listen": socksAddress, "server": cfg.Server,
		"connect_timeout": "3s", "handshake_timeout": "3s", "idle_timeout": "10s",
		"client_id": cfg.ClientID, "secret": cfg.Secret,
		"log_size": "0M", "log_file": filepath.Join(temp, "client.log"),
		"obfs": map[string]any{"provider": cfg.Obfs.Provider, "participation": cfg.Obfs.Participation},
	})
	var output bytes.Buffer
	client := exec.Command(clientBinary, "-config", path)
	client.Stdout, client.Stderr = &output, &output
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopProcess(client)
	waitTCP(t, socksAddress, &output)

	conn, err := net.DialTimeout("tcp", socksAddress, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	port := uint16(echo.Addr().(*net.TCPAddr).Port)
	request := append([]byte{5, 1, 0, 5, 1, 0, 1, 127, 0, 0, 1}, binary.BigEndian.AppendUint16(nil, port)...)
	if _, err = conn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 12)
	if _, err = io.ReadFull(conn, reply); err != nil || reply[3] != 0 {
		t.Fatalf("SOCKS CONNECT reply %v, err %v\nserver:\n%s\nclient:\n%s", reply, err, srv.output, &output)
	}

	payload := make([]byte, 256<<10)
	_, _ = rand.Read(payload)
	go func() {
		_, _ = conn.Write(payload)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo returned %d bytes, want %d identical bytes", len(got), len(payload))
	}
}
