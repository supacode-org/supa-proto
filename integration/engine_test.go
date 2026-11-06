package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"supaproto/internal/config"
	"supaproto/internal/engine"
	"supaproto/internal/router"
	"supaproto/internal/tun/tuntest"
)

// appDNS is any address on port 53; the engine answers every DNS query.
var appDNS = netip.MustParseAddrPort("10.7.0.53:53")

type recorder struct {
	mu      sync.Mutex
	notices []string
	logs    []string
}

func (r *recorder) notice(kind, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, kind+": "+message)
}

func (r *recorder) log(_ int, tag, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, tag+": "+message)
}

func (r *recorder) waitNotice(t *testing.T, kind string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, n := range r.notices {
			if strings.HasPrefix(n, kind+":") {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no %q notice; notices=%v\nlogs:\n%s", kind, r.notices, strings.Join(r.logs, "\n"))
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return "notices: " + strings.Join(r.notices, "; ") + "\nlogs:\n" + strings.Join(r.logs, "\n")
}

func (r *recorder) logged(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func startEngine(t *testing.T, cfg config.Client, rules []router.Rule) (*tuntest.App, *recorder) {
	t.Helper()
	// Unused by these tests: no rule needs a local lookup.
	return startEngineWithDNS(t, cfg, rules, "https://127.0.0.1:1/dns-query", "127.0.0.1:1")
}

func startEngineWithDNS(t *testing.T, cfg config.Client, rules []router.Rule, doh, bootstrap string) (*tuntest.App, *recorder) {
	t.Helper()
	dev, app, err := tuntest.New()
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	e, err := engine.Start(dev, engine.Config{
		Client: cfg, Rules: rules, DoHURL: doh, Bootstrap: bootstrap,
		Log: rec.log, Notice: rec.notice,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.Close()
		_ = e.Close()
	})
	return app, rec
}

func mustRules(t *testing.T, text string) []router.Rule {
	t.Helper()
	rules, err := router.ParseRules(text)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// lookup asks the engine's DNS for name and returns the A record.
func lookup(t *testing.T, app *tuntest.App, name string) netip.Addr {
	t.Helper()
	addr, err := resolveThroughEngine(app, name)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func resolveThroughEngine(app *tuntest.App, name string) (netip.Addr, error) {
	conn, err := app.DialUDP(appDNS)
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	query, _ := (&dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 7, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}).Pack()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = conn.Write(query); err != nil {
		return netip.Addr{}, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("DNS response: %w", err)
	}
	var m dnsmessage.Message
	if err = m.Unpack(buf[:n]); err != nil || len(m.Answers) != 1 {
		return netip.Addr{}, fmt.Errorf("DNS answer %+v, err %v", m, err)
	}
	return netip.AddrFrom4(m.Answers[0].Body.(*dnsmessage.AResource).A), nil
}

func echoTCP(t *testing.T, conn net.Conn, size int) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	payload := make([]byte, size)
	_, _ = rand.Read(payload)
	go func() {
		_, _ = conn.Write(payload)
		_ = conn.(interface{ CloseWrite() error }).CloseWrite()
	}()
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo returned %d bytes, want %d identical bytes", len(got), len(payload))
	}
}

func startUDPEcho(t *testing.T, ip net.IP) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], peer)
		}
	}()
	return conn
}

func udpRoundTrip(t *testing.T, conn net.Conn, message string, context ...fmt.Stringer) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(message)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != message {
		t.Fatalf("UDP echo got %q, err %v\n%v", buf[:n], err, context)
	}
}

// lanIP returns a private IPv4 address of this machine. Loopback cannot be
// used: netstack drops loopback destinations arriving on a non-loopback NIC.
func lanIP(t *testing.T) net.IP {
	t.Helper()
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.To4()
		}
	}
	t.Skip("no private IPv4 address for DIRECT tests")
	return nil
}

func TestEngineProxiesTCPAndUDPByDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping engine integration test")
	}
	srv := startServer(t)
	tcpEcho := startTCPEcho(t)
	// "localhost" resolves to both ::1 and 127.0.0.1; the server must pick
	// IPv4 first for UDP, so an IPv4-only echo has to receive the datagram.
	udpEcho := startUDPEcho(t, net.IPv4(127, 0, 0, 1))
	app, rec := startEngine(t, srv.clientConfig(), mustRules(t, "FINAL,PROXY"))
	rec.waitNotice(t, engine.NoticeConnected)

	// The server, not the device, resolves "localhost": the domain is kept.
	fake := lookup(t, app, "localhost")
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(fake) {
		t.Fatalf("lookup returned %v, want a fake IP", fake)
	}
	conn, err := app.DialTCP(context.Background(), netip.AddrPortFrom(fake, uint16(tcpEcho.Addr().(*net.TCPAddr).Port)))
	if err != nil {
		t.Fatalf("dial through engine: %v\nserver:\n%s", err, srv.output)
	}
	defer conn.Close()
	echoTCP(t, conn, 512<<10)
	if !rec.logged("tcp localhost:") {
		t.Fatalf("engine did not route by domain; logs: %v", rec.logs)
	}

	udp, err := app.DialUDP(netip.AddrPortFrom(fake, uint16(udpEcho.LocalAddr().(*net.UDPAddr).Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udpRoundTrip(t, udp, "udp through proxy", rec, srv.output)
}

func TestEngineDirectRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping engine integration test")
	}
	ip := lanIP(t)
	srv := startServer(t)
	rules, _ := router.ModeRules(router.ModeGlobal) // private addresses go direct
	app, rec := startEngine(t, srv.clientConfig(), rules)

	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.AcceptTCP()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c); _ = c.CloseWrite() }()
		}
	}()
	target, _ := netip.AddrFromSlice(ip)
	conn, err := app.DialTCP(context.Background(), netip.AddrPortFrom(target, uint16(l.Addr().(*net.TCPAddr).Port)))
	if err != nil {
		t.Fatalf("direct dial: %v", err)
	}
	defer conn.Close()
	echoTCP(t, conn, 128<<10)
	if !rec.logged("DIRECT via IP-CIDR") {
		t.Fatalf("flow did not go DIRECT; logs: %v", rec.logs)
	}

	udpEcho := startUDPEcho(t, ip)
	udp, err := app.DialUDP(netip.AddrPortFrom(target, uint16(udpEcho.LocalAddr().(*net.UDPAddr).Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udpRoundTrip(t, udp, "udp direct")
}

func TestEngineRejectRule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping engine integration test")
	}
	srv := startServer(t)
	app, _ := startEngine(t, srv.clientConfig(), mustRules(t, "DOMAIN,blocked.test,REJECT\nFINAL,PROXY"))
	fake := lookup(t, app, "blocked.test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if conn, err := app.DialTCP(ctx, netip.AddrPortFrom(fake, 443)); err == nil {
		conn.Close()
		t.Fatal("REJECT rule allowed the connection")
	}
}

// With the server down, proxied flows are refused and never sent directly.
func TestEngineKillSwitchBlocksWhenServerDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping engine integration test")
	}
	cfg := testServer{address: reserveTCPAddress(t), secret: "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI"}.clientConfig()
	cfg.ConnectTimeout = "1s"
	ip := lanIP(t)
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	// Everything is proxied, even the private address.
	app, rec := startEngine(t, cfg, mustRules(t, "FINAL,PROXY"))
	rec.waitNotice(t, engine.NoticeConnectionFailed)

	target, _ := netip.AddrFromSlice(ip)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if conn, err := app.DialTCP(ctx, netip.AddrPortFrom(target, uint16(l.Addr().(*net.TCPAddr).Port))); err == nil {
		conn.Close()
		t.Fatal("connection succeeded with the proxy down")
	}
	if accepted.Load() != 0 {
		t.Fatal("traffic leaked directly to the target")
	}
}
