package integration

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"supaproto/internal/config"
	"supaproto/internal/engine"
	"supaproto/internal/router"
	"supaproto/internal/tun/tuntest"
)

// These tests reach a real SupaProto server and the public internet. They
// only run when SUPAPROTO_REAL_CLIENT_CONFIG points at a client.json.
func realClientConfig(t *testing.T) config.Client {
	t.Helper()
	path := os.Getenv("SUPAPROTO_REAL_CLIENT_CONFIG")
	if path == "" {
		t.Skip("set SUPAPROTO_REAL_CLIENT_CONFIG to run against a real server")
	}
	var cfg config.Client
	if err := config.Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// httpThroughEngine sends an HTTP GET from the simulated app, resolving the
// host with the engine's DNS like a real app would.
func httpThroughEngine(app *tuntest.App, url string) (int, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addr, err := resolveThroughEngine(app, host)
			if err != nil {
				return nil, err
			}
			p, _ := strconv.Atoi(port)
			return app.DialTCP(ctx, netip.AddrPortFrom(addr, uint16(p)))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func routeOf(rec *recorder, proto, host string) string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, l := range rec.logs {
		if i := strings.Index(l, proto+" "+host+":"); i >= 0 {
			return l[i:]
		}
	}
	return "(no route logged)"
}

func TestRealServerGlobalMode(t *testing.T) {
	cfg := realClientConfig(t)
	rules, _ := router.ModeRules(router.ModeGlobal)
	app, rec := startEngineWithDNS(t, cfg, rules, "https://dns.google/dns-query", "8.8.8.8:53")
	rec.waitNotice(t, engine.NoticeConnected)

	status, err := httpThroughEngine(app, "http://www.gstatic.com/generate_204")
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("generate_204: status %d, err %v\n%s", status, err, rec)
	}
	t.Logf("route: %s", routeOf(rec, "tcp", "www.gstatic.com"))

	// UDP through the proxy by domain: one SNTP request. The engine hands the
	// name to the server, so this also covers remote-DNS response handling.
	ntp := lookup(t, app, "time.google.com")
	conn, err := app.DialUDP(netip.AddrPortFrom(ntp, 123))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := make([]byte, 48)
	request[0] = 0x1b // LI 0, version 3, client mode
	if _, err = conn.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 128)
	n, err := conn.Read(reply)
	if err != nil || n < 48 {
		t.Fatalf("NTP reply: %d bytes, err %v\n%s", n, err, rec)
	}
	seconds := binary.BigEndian.Uint32(reply[40:44])
	serverTime := time.Unix(int64(seconds)-2208988800, 0)
	if d := time.Since(serverTime); d > time.Hour || d < -time.Hour {
		t.Fatalf("NTP time %v is far from local time", serverTime)
	}
	t.Logf("route: %s; NTP time %s", routeOf(rec, "udp", "time.google.com"), serverTime.UTC().Format(time.RFC3339))
	if rec.logged("Engine: tcp") && !rec.logged("PROXY via FINAL") {
		t.Fatalf("global mode did not proxy\n%s", rec)
	}
}

func TestRealServerGeoIPCNMode(t *testing.T) {
	cfg := realClientConfig(t)
	rules, _ := router.ModeRules(router.ModeGeoIPCN)
	app, rec := startEngineWithDNS(t, cfg, rules, "https://dns.alidns.com/dns-query", "8.8.8.8:53")
	rec.waitNotice(t, engine.NoticeConnected)

	for _, site := range []string{"http://www.baidu.com/", "http://www.gstatic.com/generate_204"} {
		status, err := httpThroughEngine(app, site)
		if err != nil || status >= 400 {
			t.Fatalf("%s: status %d, err %v\n%s", site, status, err, rec)
		}
		host := strings.Split(strings.TrimPrefix(site, "http://"), "/")[0]
		t.Logf("%s -> HTTP %d, route: %s", site, status, routeOf(rec, "tcp", host))
	}
}
