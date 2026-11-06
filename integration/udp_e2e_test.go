package integration

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"supaproto/internal/socks5"
)

func TestUDPOverTCPEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-level UDP integration test")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	temp := t.TempDir()
	serverBinary := filepath.Join(temp, "supaproto-server")
	clientBinary := filepath.Join(temp, "supaproto-client")
	build(t, root, serverBinary, "./cmd/supaproto-server")
	build(t, root, clientBinary, "./cmd/supaproto-client")

	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, peer, readErr := echo.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = echo.WriteToUDP(buffer[:n], peer)
		}
	}()

	serverAddress := reserveTCPAddress(t)
	clientAddress := reserveTCPAddress(t)
	provider := os.Getenv("SUPAPROTO_TEST_OBFS")
	participation := 0.0
	if provider == "" {
		provider = "none"
	}
	if provider != "none" {
		participation = 0.65
	}
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	serverConfig := map[string]any{
		"listen": serverAddress, "handshake_timeout": "3s", "dial_timeout": "3s", "idle_timeout": "10s",
		"log_size": "0M", "log_file": filepath.Join(temp, "server.log"),
		"obfs": map[string]any{"provider": provider, "participation": participation},
		"udp": map[string]any{
			"enabled": true, "idle_timeout": "10s", "max_datagram_size": 61440, "queue_packets": 32,
			"max_associations_per_user": 8, "max_destinations_per_association": 32,
			"packets_per_second_per_user": 1000, "bytes_per_second_per_user": 1048576,
		},
		"users": []any{map[string]any{
			"client_id": "integration-user", "secret": secret, "status": "enabled",
			"max_connections": 8, "allow_private": true,
		}},
	}
	clientConfig := map[string]any{
		"socks_listen": clientAddress, "server": serverAddress,
		"connect_timeout": "3s", "handshake_timeout": "3s", "idle_timeout": "10s",
		"client_id": "integration-user", "secret": secret,
		"log_size": "0M", "log_file": filepath.Join(temp, "client.log"),
		"obfs": map[string]any{"provider": provider, "participation": participation},
		"udp": map[string]any{
			"enabled": true, "bind": "127.0.0.1:0", "idle_timeout": "10s",
			"max_datagram_size": 61440, "queue_packets": 32,
		},
	}
	serverConfigPath := writeJSON(t, temp, "server.json", serverConfig)
	clientConfigPath := writeJSON(t, temp, "client.json", clientConfig)

	var serverOutput, clientOutput bytes.Buffer
	server := exec.Command(serverBinary, "-config", serverConfigPath)
	server.Stdout, server.Stderr = &serverOutput, &serverOutput
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopProcess(server)
	waitTCP(t, serverAddress, &serverOutput)

	client := exec.Command(clientBinary, "-config", clientConfigPath)
	client.Stdout, client.Stderr = &clientOutput, &clientOutput
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopProcess(client)
	waitTCP(t, clientAddress, &clientOutput)

	control, err := net.DialTimeout("tcp", clientAddress, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	selection := make([]byte, 2)
	if _, err = io.ReadFull(control, selection); err != nil || !bytes.Equal(selection, []byte{5, 0}) {
		t.Fatalf("SOCKS greeting failed: %v %v", selection, err)
	}
	if _, err = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	relayAddress := readSOCKSReply(t, control)

	application, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	request, err := socks5.EncodeUDPDatagram("127.0.0.1", uint16(echo.LocalAddr().(*net.UDPAddr).Port), []byte("udp-over-tcp"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.WriteToUDP(request, relayAddress); err != nil {
		t.Fatal(err)
	}
	_ = application.SetReadDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, 65535)
	n, _, err := application.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("UDP response timeout: %v\nserver:\n%s\nclient:\n%s", err, serverOutput.String(), clientOutput.String())
	}
	host, port, data, err := socks5.ParseUDPDatagram(buffer[:n])
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != uint16(echo.LocalAddr().(*net.UDPAddr).Port) || string(data) != "udp-over-tcp" {
		t.Fatalf("unexpected UDP response: %s:%d %q", host, port, data)
	}
}

func TestClientUDPDisabledRejectsWithoutServerConnection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	temp := t.TempDir()
	clientBinary := filepath.Join(temp, "supaproto-client")
	build(t, root, clientBinary, "./cmd/supaproto-client")
	clientAddress := reserveTCPAddress(t)
	disabled := false
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))
	clientConfig := map[string]any{
		"socks_listen": clientAddress, "server": "127.0.0.1:1",
		"connect_timeout": "1s", "handshake_timeout": "1s", "idle_timeout": "10s",
		"client_id": "integration-user", "secret": secret,
		"log_size": "0M", "log_file": filepath.Join(temp, "client.log"),
		"obfs": map[string]any{"provider": "none", "participation": 0},
		"udp": map[string]any{
			"enabled": disabled, "bind": "127.0.0.1:0", "idle_timeout": "10s",
			"max_datagram_size": 61440, "queue_packets": 32,
		},
	}
	configPath := writeJSON(t, temp, "client.json", clientConfig)
	var output bytes.Buffer
	client := exec.Command(clientBinary, "-config", configPath)
	client.Stdout, client.Stderr = &output, &output
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopProcess(client)
	waitTCP(t, clientAddress, &output)
	control, err := net.DialTimeout("tcp", clientAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = control.Write([]byte{5, 1, 0})
	selection := make([]byte, 2)
	if _, err = io.ReadFull(control, selection); err != nil {
		t.Fatal(err)
	}
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err = io.ReadFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 7 {
		t.Fatalf("UDP disabled reply=%d, want command-not-supported (7)", reply[1])
	}
}

func build(t *testing.T, root, output, pkg string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, pkg)
	command.Dir = root
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, result)
	}
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func writeJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitTCP(t *testing.T, address string, output *bytes.Buffer) {
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
	t.Fatalf("process did not listen on %s:\n%s", address, output.String())
}

func readSOCKSReply(t *testing.T, control net.Conn) *net.UDPAddr {
	t.Helper()
	reader := bufio.NewReader(control)
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		t.Fatalf("SOCKS UDP ASSOCIATE failed: %v", header)
	}
	var ip net.IP
	switch header[3] {
	case 1:
		ip = make(net.IP, 4)
	case 4:
		ip = make(net.IP, 16)
	default:
		t.Fatalf("unexpected relay address type: %d", header[3])
	}
	if _, err := io.ReadFull(reader, ip); err != nil {
		t.Fatal(err)
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		t.Fatal(err)
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		t.Fatal("SOCKS returned zero UDP relay port")
	}
	return &net.UDPAddr{IP: ip, Port: int(port)}
}

func stopProcess(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = command.Process.Kill()
	_, _ = command.Process.Wait()
}
