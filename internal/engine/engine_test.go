package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"supaproto/internal/config"
	"supaproto/internal/router"
	"supaproto/internal/tun/tuntest"
)

var tunnelDNS = netip.MustParseAddr("172.19.0.2")

// Outside dials hang like an unreachable host, so only an immediate refusal
// by the engine ends a connection quickly.
func hang(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, errors.New("network disabled in test")
}

func startTestEngine(t *testing.T) *tuntest.App {
	t.Helper()
	rules, err := router.ModeRules(router.ModeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	_, app := startEngineWith(t, rules, hang)
	return app
}

func startEngineWith(t *testing.T, rules []router.Rule, dial func(context.Context, string, string) (net.Conn, error)) (*Engine, *tuntest.App) {
	t.Helper()
	dev, app, err := tuntest.New()
	if err != nil {
		t.Fatal(err)
	}
	e, err := Start(dev, Config{
		Client: config.Client{
			Server:   "127.0.0.1:1",
			ClientID: "test",
			Secret:   "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI",
		},
		Rules:          rules,
		DoHURL:         "https://dns.test/dns-query",
		Bootstrap:      "127.0.0.1:53",
		TunnelPrefixes: []netip.Prefix{netip.MustParsePrefix("172.19.0.0/30")},
		Dial:           dial,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = e.Close()
		app.Close()
	})
	return e, app
}

// Android's Private DNS tries DoT on port 853 at the tunnel DNS address. It
// must be refused at once so the system falls back to plain DNS.
func TestRefusesTCPToTunnelAddress(t *testing.T) {
	app := startTestEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := app.DialTCP(ctx, netip.AddrPortFrom(tunnelDNS, 853))
	if err == nil {
		conn.Close()
		t.Fatal("connection to the tunnel address was accepted")
	}
	if ctx.Err() != nil {
		t.Fatalf("connection hung until the deadline: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("refusal took %v", elapsed)
	}
}

func TestStillAnswersDNSAtTunnelAddress(t *testing.T) {
	app := startTestEngine(t)
	conn, err := app.DialUDP(netip.AddrPortFrom(tunnelDNS, 53))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query, err := (&dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 7, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}).Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no DNS answer: %v", err)
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(buf[:n]); err != nil || resp.ID != 7 || len(resp.Answers) == 0 {
		t.Fatalf("answer = %+v, %v", resp, err)
	}
}

// After a network switch every open flow is closed, so apps reconnect over
// the new network instead of keeping connections set up over the old one.
func TestNetworkChangeClosesOpenFlows(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c) }()
		}
	}()
	rules, err := router.ParseRules("FINAL,DIRECT")
	if err != nil {
		t.Fatal(err)
	}
	// Every direct dial reaches the local echo server.
	toEcho := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
	}
	e, app := startEngineWith(t, rules, toEcho)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := app.DialTCP(ctx, netip.MustParseAddrPort("192.0.2.10:80"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("echo through the engine: %v", err)
	}

	e.NetworkChanged()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(buf)
	var netErr net.Error
	if err == nil || errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("flow still open after the network changed: %v", err)
	}
}
