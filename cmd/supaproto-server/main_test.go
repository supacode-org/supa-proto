package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"supaproto/internal/config"
	"supaproto/internal/obfs"
	"supaproto/internal/protocol"
)

func TestWritePresetError(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	provider, err := obfs.Load(obfs.Config{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := provider.NewConn(server, "s2c")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		writePresetError(wire, errorInvalidRequest, "invalid request")
		_ = server.Close()
		close(done)
	}()
	env, err := protocol.ReadLine(bufio.NewReader(client))
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != "error" || env.Code != errorInvalidRequest || env.Message != "invalid request" {
		t.Fatalf("unexpected error envelope: %#v", env)
	}
	<-done
}

func TestDialRejectsPrivateTarget(t *testing.T) {
	a := &app{dialTimeout: time.Second}
	_, _, err := a.dial("127.0.0.1", 80, false)
	if !errors.Is(err, errPrivateTarget) {
		t.Fatalf("expected private target rejection, got %v", err)
	}
}

type fakeOutboundDialer struct {
	conn    net.Conn
	network string
	target  string
}

func (d *fakeOutboundDialer) DialContext(_ context.Context, network, target string) (net.Conn, error) {
	d.network, d.target = network, target
	return d.conn, nil
}

func TestDialRoutesResolvedIPThroughOutbound(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	outbound := &fakeOutboundDialer{conn: client}
	a := &app{
		dialTimeout: time.Second,
		outbound:    outbound,
		cfg: config.Server{Outbound: config.Outbound{
			Type: "socks5", Address: "127.0.0.1:1080", DNS: "local",
		}},
	}
	conn, resolved, err := a.dial("8.8.8.8", 443, false)
	if err != nil {
		t.Fatal(err)
	}
	if conn != client || outbound.network != "tcp" || outbound.target != "8.8.8.8:443" || resolved != "8.8.8.8:443" {
		t.Fatalf("unexpected outbound route: network=%q target=%q resolved=%q", outbound.network, outbound.target, resolved)
	}
	if a.egressType() != "socks5" || a.egressProxy() != "127.0.0.1:1080" {
		t.Fatal("unexpected egress log fields")
	}
}

func TestDialRoutesDomainThroughRemoteOutbound(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	outbound := &fakeOutboundDialer{conn: client}
	a := &app{
		dialTimeout: time.Second,
		outbound:    outbound,
		cfg: config.Server{Outbound: config.Outbound{
			Type: "socks5", Address: "127.0.0.1:1080", DNS: "remote",
		}},
	}
	conn, target, err := a.dial("does-not-resolve.invalid", 443, false)
	if err != nil {
		t.Fatal(err)
	}
	if conn != client || outbound.network != "tcp" || outbound.target != "does-not-resolve.invalid:443" || target != "does-not-resolve.invalid:443" {
		t.Fatalf("unexpected remote outbound route: network=%q target=%q resolved=%q", outbound.network, outbound.target, target)
	}
}

func TestClaimDistinguishesReplayAndQuota(t *testing.T) {
	a := &app{
		replay: make(map[string]time.Time),
		active: make(map[string]int),
	}
	release, rejection := a.claim("user", "nonce-1", 1)
	if rejection != "" || release == nil {
		t.Fatalf("first claim rejected: %q", rejection)
	}
	if _, rejection = a.claim("user", "nonce-2", 1); rejection != "quota" {
		t.Fatalf("expected quota rejection, got %q", rejection)
	}
	release()
	if _, rejection = a.claim("user", "nonce-1", 1); rejection != "replay" {
		t.Fatalf("expected replay rejection, got %q", rejection)
	}
}
