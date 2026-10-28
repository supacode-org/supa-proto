// Package engine ties the TUN stack to DNS, routing and the JSON2Proxy
// client: it answers DNS with fake IPs, routes each flow and relays it
// directly or through the proxy. Proxied traffic never falls back to a
// direct connection, so a broken proxy blocks traffic instead of leaking it.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"json2proxy/internal/client"
	"json2proxy/internal/config"
	"json2proxy/internal/dns"
	"json2proxy/internal/fakeip"
	"json2proxy/internal/router"
	"json2proxy/internal/stats"
	"json2proxy/internal/tun"
)

// Log levels, matching the mobile API.
const (
	LogDebug = 0
	LogInfo  = 1
	LogWarn  = 2
	LogError = 3
)

// Notice kinds. Each must also be registered in the app notice spec. The
// message passed with a notice is a detail for the app to place in its own
// localized text: the server address for NoticeConnected, the error otherwise.
const (
	NoticeConnected        = "connected"
	NoticeConnectionFailed = "connection_failed"
	NoticeProxyUnavailable = "proxy_unavailable"
	NoticeBootstrapFailed  = "bootstrap_failed"
	NoticeDoHUnavailable   = "doh_unavailable"
)

const defaultMTU = 1500

type Config struct {
	Client    config.Client
	Rules     []router.Rule
	DoHURL    string
	Bootstrap string
	MTU       int
	// TunnelPrefixes are the TUN interface's own networks. Only DNS on port
	// 53 is served there; other flows to them are refused.
	TunnelPrefixes []netip.Prefix
	// Dial opens sockets that bypass the VPN tunnel. Nil uses net.Dialer.
	Dial dns.DialFunc
	// Log and Notice may be nil.
	Log    func(level int, tag, message string)
	Notice func(kind, message string)
}

type Engine struct {
	cfg    Config
	dial   dns.DialFunc
	boot   *dns.Bootstrap
	doh    *dns.DoH
	client *client.Client
	router *router.Router
	pool   *fakeip.Pool
	stack  *tun.Stack
	stats  *stats.Collector
	cover  *client.Cover
	conns  connSet
	ctx    context.Context
	cancel context.CancelFunc

	healthMu       sync.Mutex
	proxyAvailable bool
	proxyKnown     bool
}

// Start brings up the engine on dev, which it takes ownership of. It does not
// wait for the server: an unreachable server blocks proxied traffic and is
// reported through Notice.
func Start(dev tun.Device, cfg Config) (*Engine, error) {
	if cfg.MTU == 0 {
		cfg.MTU = defaultMTU
	}
	e := &Engine{cfg: cfg, dial: cfg.Dial, stats: stats.New()}
	if e.dial == nil {
		e.dial = (&net.Dialer{}).DialContext
	}
	var err error
	if e.boot, err = dns.NewBootstrap(cfg.Bootstrap, e.dial); err != nil {
		_ = dev.Close()
		return nil, err
	}
	e.doh, err = dns.NewDoH(dns.DoHConfig{
		URL: cfg.DoHURL, Bootstrap: e.boot, Dial: e.dial, OnHealthChange: e.dohHealthChanged,
	})
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	e.client, err = client.New(cfg.Client, client.Options{Dial: e.dial, Resolve: e.boot.LookupIP})
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	if e.router, err = router.New(cfg.Rules, e.doh); err != nil {
		_ = dev.Close()
		return nil, err
	}
	if e.pool, err = fakeip.NewPool(fakeip.DefaultPrefix, fakeip.DefaultCapacity); err != nil {
		_ = dev.Close()
		return nil, err
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	if e.stack, err = tun.New(dev, cfg.MTU, e); err != nil {
		e.cancel()
		_ = dev.Close()
		return nil, err
	}
	if cfg.Client.Cover.Enabled {
		e.cover = e.client.StartCover(e.ctx, client.CoverOptions{
			RateKbps: cfg.Client.Cover.RateKbps,
			Log:      cfg.Log,
			Stats:    coverStats{e.stats},
		})
	}
	e.logf(LogInfo, "started server=%s mtu=%d doh=%s bootstrap=%s cover=%t", e.client.Config().Server, cfg.MTU, cfg.DoHURL, cfg.Bootstrap, cfg.Client.Cover.Enabled)
	go e.probe()
	return e, nil
}

// coverStats forwards cover-manager events to the statistics collector so the
// app can show cover connections and bytes apart from real flows.
type coverStats struct{ c *stats.Collector }

func (s coverStats) CoverSessionOpened()     { s.c.AddCoverConn(1) }
func (s coverStats) CoverSessionClosed()     { s.c.AddCoverConn(-1) }
func (s coverStats) CoverBytes(up, down int) { s.c.AddCoverBytes(up, down) }

// Close ends every flow, stops the stack, closes the device and drops all
// mappings.
func (e *Engine) Close() error {
	e.cancel()
	if e.cover != nil {
		e.cover.Close()
	}
	e.conns.closeAll(true)
	err := e.stack.Close()
	e.pool.Reset()
	e.logf(LogInfo, "stopped")
	return err
}

// Stats returns the traffic counters; detail adds hosts, DNS results and
// recent flows.
func (e *Engine) Stats(detail bool) stats.Snapshot { return e.stats.Snapshot(detail) }

// NetworkChanged drops cached DNS answers and closes every open flow after
// the device switches networks, so no connection keeps using a path set up
// over the previous network; apps reconnect through the current one.
func (e *Engine) NetworkChanged() {
	e.boot.ClearCache()
	e.doh.ClearCache()
	closed := e.conns.closeAll(false)
	if e.cover != nil {
		e.cover.NetworkChanged()
	}
	e.logf(LogInfo, "network changed, DNS caches cleared, %d connections closed", closed)
}

func (e *Engine) probe() {
	err := e.client.Probe(e.ctx)
	if e.ctx.Err() != nil {
		return
	}
	e.healthMu.Lock()
	e.proxyKnown, e.proxyAvailable = true, err == nil
	e.healthMu.Unlock()
	server := e.client.Config().Server
	switch {
	case err == nil:
		e.logf(LogInfo, "server %s reachable", server)
		e.notice(NoticeConnected, server)
	case isBootstrapError(err):
		e.logf(LogError, "server %s: %v", server, err)
		e.notice(NoticeBootstrapFailed, err.Error())
	default:
		e.logf(LogError, "server %s: %v", server, err)
		e.notice(NoticeConnectionFailed, err.Error())
	}
}

// proxyResult records whether opening a proxied flow reached the server and
// reports changes. A target refused by the server still means the proxy works.
func (e *Engine) proxyResult(err error) {
	var refused *client.OpenError
	available := err == nil || errors.As(err, &refused) || errors.Is(err, client.ErrInvalidTarget)
	e.healthMu.Lock()
	changed := e.proxyKnown && available != e.proxyAvailable
	e.proxyKnown, e.proxyAvailable = true, available
	e.healthMu.Unlock()
	if !changed {
		return
	}
	switch {
	case available:
		e.logf(LogInfo, "server reachable again")
		e.notice(NoticeConnected, e.client.Config().Server)
	case isBootstrapError(err):
		e.notice(NoticeBootstrapFailed, err.Error())
	default:
		e.notice(NoticeProxyUnavailable, err.Error())
	}
}

func (e *Engine) dohHealthChanged(available bool, err error) {
	if available {
		e.logf(LogInfo, "DoH available again")
		return
	}
	e.logf(LogError, "DoH unavailable: %v", err)
	if isBootstrapError(err) {
		e.notice(NoticeBootstrapFailed, err.Error())
		return
	}
	e.notice(NoticeDoHUnavailable, err.Error())
}

// tunnelAddress reports whether dst belongs to the TUN interface itself.
func (e *Engine) tunnelAddress(dst netip.Addr) bool {
	dst = dst.Unmap()
	for _, p := range e.cfg.TunnelPrefixes {
		if p.Contains(dst) {
			return true
		}
	}
	return false
}

// target maps a destination back to the domain it was looked up for.
func (e *Engine) target(dst netip.Addr) (router.Target, bool) {
	if e.pool.Contains(dst) {
		name, ok := e.pool.Name(dst)
		return router.Target{Host: name}, ok
	}
	return router.Target{Addr: dst}, true
}

// route decides how to handle a flow and records the decision, plus any DoH
// lookup made while matching, in the statistics.
func (e *Engine) route(ctx context.Context, network string, t router.Target, port uint16) (router.Decision, *stats.Flow) {
	d := e.router.Route(ctx, t)
	if t.Host != "" && (len(d.Addrs) > 0 || d.ResolveErr != nil) {
		e.stats.RecordDNS(t.Host, d.Addrs, d.ResolveErr)
	}
	return d, e.stats.Route(network, proxyHost(t), port, d.Action.String(), d.Rule.String())
}

// directAddrs returns real addresses for a DIRECT flow, resolving domains
// through DoH when routing did not already do so.
func (e *Engine) directAddrs(ctx context.Context, t router.Target, d router.Decision) ([]netip.Addr, error) {
	if t.Addr.IsValid() {
		return []netip.Addr{t.Addr}, nil
	}
	if len(d.Addrs) > 0 {
		return d.Addrs, nil
	}
	addrs, err := e.doh.LookupIP(ctx, t.Host)
	e.stats.RecordDNS(t.Host, addrs, err)
	return addrs, err
}

func (e *Engine) dialDirect(ctx context.Context, network string, addrs []netip.Addr, port uint16) (net.Conn, error) {
	var errs []error
	for _, addr := range addrs {
		if e.pool.Contains(addr) {
			continue
		}
		conn, err := e.dial(ctx, network, net.JoinHostPort(addr.String(), strconv.Itoa(int(port))))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errors.New("no usable address")
	}
	return nil, errors.Join(errs...)
}

func describe(t router.Target, port uint16) string {
	host := t.Host
	if host == "" {
		host = t.Addr.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func proxyHost(t router.Target) string {
	if t.Host != "" {
		return t.Host
	}
	return t.Addr.String()
}

func isBootstrapError(err error) bool {
	var be *dns.BootstrapError
	return errors.As(err, &be)
}

func (e *Engine) logf(level int, format string, args ...any) {
	if e.cfg.Log != nil {
		e.cfg.Log(level, "Engine", fmt.Sprintf(format, args...))
	}
}

func (e *Engine) notice(kind, message string) {
	if e.cfg.Notice != nil {
		e.cfg.Notice(kind, message)
	}
}
