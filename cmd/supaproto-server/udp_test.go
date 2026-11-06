package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supaproto/internal/config"
	"supaproto/internal/socks5"
)

func TestDirectUDPEgressRoundTrip(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 1024)
		n, peer, readErr := echo.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = echo.WriteToUDP(buffer[:n], peer)
		}
	}()
	var drops atomic.Uint64
	egress, err := newDirectUDPEgress(8, 61440, &drops)
	if err != nil {
		t.Fatal(err)
	}
	defer egress.Close()
	target := echo.LocalAddr().(*net.UDPAddr)
	if err = egress.WriteDatagram(target.IP.String(), uint16(target.Port), []byte("hello")); err != nil {
		t.Fatal(err)
	}
	host, port, data, err := egress.ReadDatagram(time.Now().Add(time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != uint16(target.Port) || !bytes.Equal(data, []byte("hello")) {
		t.Fatalf("unexpected response: %s:%d %q", host, port, data)
	}
}

func TestUDPAssociationLimitAndRate(t *testing.T) {
	a := &app{
		cfg: config.Server{UDP: config.ServerUDP{
			MaxAssociationsPerUser:  1,
			PacketsPerSecondPerUser: 1,
			BytesPerSecondPerUser:   4,
		}},
		udpActive: make(map[string]int), udpRates: make(map[string]*udpRateState),
	}
	release, ok := a.claimUDP("user")
	if !ok || release == nil {
		t.Fatal("first UDP association rejected")
	}
	if _, ok = a.claimUDP("user"); ok {
		t.Fatal("UDP association limit ignored")
	}
	release()
	if !a.allowUDPRate("user", 4) || a.allowUDPRate("user", 1) {
		t.Fatal("UDP per-user rate limit not enforced")
	}
}

func TestResolveUDPDelegatesDomainToRemoteOutbound(t *testing.T) {
	a := &app{
		dialTimeout:   time.Second,
		outboundSOCKS: socks5.New("127.0.0.1:1080", "", ""),
		cfg: config.Server{Outbound: config.Outbound{
			Type: "socks5", Address: "127.0.0.1:1080", DNS: "remote",
		}},
	}
	targets, err := a.resolveUDP("does-not-resolve.invalid", 53, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != (udpTarget{host: "does-not-resolve.invalid", port: 53, key: "does-not-resolve.invalid:53"}) {
		t.Fatalf("unexpected remote UDP route: %+v", targets)
	}
}

func lookupReturning(addresses ...string) func(context.Context, string) ([]net.IP, error) {
	return func(context.Context, string) ([]net.IP, error) {
		var ips []net.IP
		for _, a := range addresses {
			ips = append(ips, net.ParseIP(a))
		}
		return ips, nil
	}
}

func targetKeys(targets []udpTarget) string {
	var keys []string
	for _, target := range targets {
		keys = append(keys, target.key)
	}
	return strings.Join(keys, " ")
}

func TestResolveUDPTriesIPv4BeforeIPv6(t *testing.T) {
	a := &app{
		dialTimeout: time.Second,
		// Resolvers on hosts with an IPv6 address usually list AAAA first.
		udpLookup: lookupReturning("2001:4860:4860::8888", "8.8.8.8", "2001:4860:4860::8844", "8.8.4.4"),
	}
	targets, err := a.resolveUDP("dns.google", 53, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "8.8.8.8:53 8.8.4.4:53 [2001:4860:4860::8888]:53 [2001:4860:4860::8844]:53"
	if got := targetKeys(targets); got != want {
		t.Fatalf("targets = %s, want %s", got, want)
	}
	if targets[0].host != "8.8.8.8" || targets[0].port != 53 {
		t.Fatalf("first target = %+v", targets[0])
	}
}

func TestResolveUDPDropsForbiddenAddresses(t *testing.T) {
	a := &app{dialTimeout: time.Second, udpLookup: lookupReturning("10.0.0.1", "2001:db8::1", "1.1.1.1")}
	targets, err := a.resolveUDP("mixed.test", 53, false)
	if err != nil || targetKeys(targets) != "1.1.1.1:53" {
		t.Fatalf("targets = %v, err %v", targetKeys(targets), err)
	}
	a = &app{dialTimeout: time.Second, udpLookup: lookupReturning("10.0.0.1", "fd00::1")}
	if _, err := a.resolveUDP("private.test", 53, false); !errors.Is(err, errPrivateTarget) {
		t.Fatalf("err = %v, want errPrivateTarget", err)
	}
	// allow_private keeps private addresses, still IPv4 first.
	targets, err = a.resolveUDP("private-allowed.test", 53, true)
	if err != nil || targetKeys(targets) != "10.0.0.1:53 [fd00::1]:53" {
		t.Fatalf("allow_private targets = %v, err %v", targetKeys(targets), err)
	}
}

type recordingEgress struct {
	fail map[string]bool
	sent []string
}

func (e *recordingEgress) WriteDatagram(host string, port uint16, _ []byte) error {
	key := net.JoinHostPort(host, strconv.Itoa(int(port)))
	e.sent = append(e.sent, key)
	if e.fail[key] {
		return errors.New("network is unreachable")
	}
	return nil
}

func (e *recordingEgress) ReadDatagram(time.Time, []byte) (string, uint16, []byte, error) {
	return "", 0, nil, errors.New("not implemented")
}

func (e *recordingEgress) Close() error { return nil }

func TestSendUDPDatagramFallsBackWhenSendFails(t *testing.T) {
	targets := []udpTarget{
		{host: "8.8.8.8", port: 53, key: "8.8.8.8:53"},
		{host: "2001:4860:4860::8888", port: 53, key: "[2001:4860:4860::8888]:53"},
	}
	allowAll := func(string) bool { return true }

	// An IPv6-only host fails IPv4 sends right away and uses IPv6.
	egress := &recordingEgress{fail: map[string]bool{"8.8.8.8:53": true}}
	target, ok := sendUDPDatagram(egress, targets, []byte("q"), allowAll)
	if !ok || target.key != "[2001:4860:4860::8888]:53" || len(egress.sent) != 2 {
		t.Fatalf("target = %+v ok = %v sent = %v", target, ok, egress.sent)
	}

	// When IPv4 works, IPv6 is never tried.
	egress = &recordingEgress{}
	target, ok = sendUDPDatagram(egress, targets, []byte("q"), allowAll)
	if !ok || target.key != "8.8.8.8:53" || len(egress.sent) != 1 {
		t.Fatalf("target = %+v ok = %v sent = %v", target, ok, egress.sent)
	}

	// Every send failing drops the datagram.
	egress = &recordingEgress{fail: map[string]bool{"8.8.8.8:53": true, "[2001:4860:4860::8888]:53": true}}
	if _, ok = sendUDPDatagram(egress, targets, []byte("q"), allowAll); ok {
		t.Fatal("datagram reported sent although every send failed")
	}

	// A full destination set skips new targets without sending.
	egress = &recordingEgress{}
	onlyKnown := func(key string) bool { return key == "[2001:4860:4860::8888]:53" }
	target, ok = sendUDPDatagram(egress, targets, []byte("q"), onlyKnown)
	if !ok || target.key != "[2001:4860:4860::8888]:53" || len(egress.sent) != 1 {
		t.Fatalf("target = %+v ok = %v sent = %v", target, ok, egress.sent)
	}
}

func TestUDPDestinationsExactMatchForNumericTargets(t *testing.T) {
	d := newUDPDestinations(8, time.Minute)
	now := time.Unix(1000, 0)
	d.sent(udpTarget{host: "8.8.8.8", port: 53, key: "8.8.8.8:53"}, now)
	if !d.allowResponse("8.8.8.8", 53, now) {
		t.Fatal("response from the exact destination rejected")
	}
	if d.allowResponse("8.8.4.4", 53, now) || d.allowResponse("8.8.8.8", 54, now) {
		t.Fatal("response from a different endpoint admitted for a numeric target")
	}
	if d.allowResponse("8.8.8.8", 53, now.Add(2*time.Minute)) {
		t.Fatal("expired destination still admitted")
	}
}

// A remote-DNS outbound answers from the IP it resolved, not from the name.
func TestUDPDestinationsLearnResponsesForRemoteNames(t *testing.T) {
	d := newUDPDestinations(2, time.Minute)
	now := time.Unix(1000, 0)
	d.sent(udpTarget{host: "time.google.com", port: 123, key: "time.google.com:123"}, now)
	if d.allowResponse("216.239.35.0", 124, now) {
		t.Fatal("response on another port admitted")
	}
	if !d.allowResponse("216.239.35.0", 123, now) {
		t.Fatal("response for a remote name rejected")
	}
	if d.count() != 2 {
		t.Fatalf("learned endpoint not recorded, count = %d", d.count())
	}
	// The learned endpoint now matches exactly; a third endpoint would
	// exceed the destination limit of 2.
	if !d.allowResponse("216.239.35.0", 123, now) || d.allowResponse("216.239.35.4", 123, now) {
		t.Fatal("destination limit not applied to learned endpoints")
	}
	if d.allowResponse("216.239.35.0", 123, now.Add(2*time.Minute)) {
		t.Fatal("expired name port still admitted")
	}
	d.prune(now.Add(2 * time.Minute))
	if d.count() != 0 {
		t.Fatalf("prune left %d endpoints", d.count())
	}
}

func TestUDPNeverAllowsMulticastOrUnspecified(t *testing.T) {
	for _, value := range []string{"0.0.0.0", "255.255.255.255", "224.0.0.1", "::", "ff02::1"} {
		if !udpNeverAllowed(net.ParseIP(value)) {
			t.Fatalf("unsafe UDP target accepted: %s", value)
		}
	}
	if udpNeverAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback must remain available when allow_private=true")
	}
}
