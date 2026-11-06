package mobile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"runtime/debug"
	"sync"

	"supaproto/internal/config"
	"supaproto/internal/engine"
	"supaproto/internal/router"
	"supaproto/internal/stats"
	"supaproto/internal/tun"
)

// Notice kinds sent by the running engine, in addition to the ones above.
const (
	NoticeConnected        = engine.NoticeConnected
	NoticeConnectionFailed = engine.NoticeConnectionFailed
	NoticeProxyUnavailable = engine.NoticeProxyUnavailable
)

// ModeCustom routes with the rules passed in the engine configuration.
const ModeCustom = "custom"

// Addresses of the TUN interface. Both platforms configure the interface
// with these, and the engine refuses flows to them other than DNS.
const (
	TunnelIPv4Address      = "172.19.0.1"
	TunnelIPv4PrefixLength = 30
	TunnelIPv6Address      = "fdfe:dcba:9876::1"
	TunnelIPv6PrefixLength = 126
	// TunnelDNSAddress is the DNS server announced to the system. The engine
	// answers DNS on port 53 at any address.
	TunnelDNSAddress = "172.19.0.2"
)

var tunnelPrefixes = []netip.Prefix{
	netip.PrefixFrom(netip.MustParseAddr(TunnelIPv4Address), TunnelIPv4PrefixLength).Masked(),
	netip.PrefixFrom(netip.MustParseAddr(TunnelIPv6Address), TunnelIPv6PrefixLength).Masked(),
}

// engineConfig is the JSON accepted by StartEngine:
//
//	{"client": {...client.json...}, "mode": "global", "rules": "",
//	 "doh": "", "bootstrap": "", "mtu": 1500, "memory_limit_mb": 0,
//	 "totals_file": ""}
//
// Empty fields take their defaults; "rules" is only used with mode "custom".
// memory_limit_mb sets the Go soft memory limit, which keeps the iOS packet
// tunnel extension under its memory cap; 0 leaves the runtime default.
// totals_file keeps the upload and download totals across engine runs; empty
// keeps none.
type engineConfig struct {
	Client        config.Client `json:"client"`
	Mode          string        `json:"mode"`
	Rules         string        `json:"rules"`
	DoH           string        `json:"doh"`
	Bootstrap     string        `json:"bootstrap"`
	MTU           int           `json:"mtu"`
	MemoryLimitMB int           `json:"memory_limit_mb"`
	TotalsFile    string        `json:"totals_file"`
}

// hostOptions are the engine settings that belong to the hosting process
// rather than to the engine itself.
type hostOptions struct {
	memoryLimit int64 // bytes; 0 leaves the runtime default
	totalsFile  string
}

var (
	engineMu sync.Mutex
	running  *engine.Engine
	totals   *totalsRecorder
)

// statsReply is the JSON returned by Stats: the engine snapshot plus the
// totals of every run.
type statsReply struct {
	stats.Snapshot
	TotalUp   int64 `json:"total_up"`
	TotalDown int64 `json:"total_down"`
}

// StartEngine starts the network engine on a TUN file descriptor, which it
// takes ownership of. Only one engine runs at a time.
func StartEngine(tunFd int, configJSON string) error {
	cfg, host, err := parseEngineConfig(configJSON)
	if err != nil {
		// StartEngine owns tunFd even when it fails.
		_ = tun.NewFDDevice(tunFd).Close()
		return err
	}
	if host.memoryLimit > 0 {
		debug.SetMemoryLimit(host.memoryLimit)
	}
	return startEngine(tun.NewFDDevice(tunFd), cfg, host.totalsFile)
}

func startEngine(dev tun.Device, cfg engine.Config, totalsFile string) error {
	engineMu.Lock()
	defer engineMu.Unlock()
	if running != nil {
		_ = dev.Close()
		return errors.New("engine already running")
	}
	cfg.Dial = dialProtected
	cfg.Log = logf
	cfg.Notice = func(kind, message string) {
		if p := current(); p != nil {
			p.Notice(kind, message)
		}
	}
	e, err := engine.Start(dev, cfg)
	if err != nil {
		return err
	}
	running = e
	totals = startTotals(totalsFile, func() trafficTotals {
		s := e.Stats(false)
		return trafficTotals{Up: s.Up, Down: s.Down}
	})
	return nil
}

// StopEngine stops the running engine, if any.
func StopEngine() {
	engineMu.Lock()
	e, t := running, totals
	running, totals = nil, nil
	engineMu.Unlock()
	if e != nil {
		_ = e.Close()
		t.close()
	}
}

// EngineRunning reports whether an engine is running.
func EngineRunning() bool {
	engineMu.Lock()
	defer engineMu.Unlock()
	return running != nil
}

// Stats returns a JSON snapshot of the running engine's traffic (see the
// traffic statistics spec), or "" when no engine runs. detail adds hosts, DNS
// results and recent flows; the summary is enough for a once-per-second poll.
func Stats(detail bool) string {
	engineMu.Lock()
	e, t := running, totals
	engineMu.Unlock()
	if e == nil {
		return ""
	}
	sum := t.current()
	data, err := json.Marshal(statsReply{Snapshot: e.Stats(detail), TotalUp: sum.Up, TotalDown: sum.Down})
	if err != nil {
		return ""
	}
	return string(data)
}

// NetworkChanged tells the engine the device switched networks.
func NetworkChanged() {
	engineMu.Lock()
	e := running
	engineMu.Unlock()
	if e != nil {
		e.NetworkChanged()
	}
}

// parseEngineConfig returns the engine configuration and the settings of
// the hosting process.
func parseEngineConfig(configJSON string) (engine.Config, hostOptions, error) {
	var raw engineConfig
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return engine.Config{}, hostOptions{}, fmt.Errorf("parse engine config: %w", err)
	}
	if raw.MemoryLimitMB < 0 {
		return engine.Config{}, hostOptions{}, errors.New("memory_limit_mb must not be negative")
	}
	if raw.Mode == "" {
		raw.Mode = ModeGlobal
	}
	var rules []router.Rule
	var err error
	if raw.Mode == ModeCustom {
		rules, err = router.ParseRules(raw.Rules)
	} else {
		rules, err = router.ModeRules(router.Mode(raw.Mode))
	}
	if err != nil {
		return engine.Config{}, hostOptions{}, err
	}
	if raw.DoH == "" {
		raw.DoH = DefaultDoH(raw.Mode)
	}
	if raw.Bootstrap == "" {
		raw.Bootstrap = DefaultBootstrap
	}
	return engine.Config{
		Client: raw.Client, Rules: rules, DoHURL: raw.DoH, Bootstrap: raw.Bootstrap, MTU: raw.MTU,
		TunnelPrefixes: tunnelPrefixes,
	}, hostOptions{memoryLimit: int64(raw.MemoryLimitMB) << 20, totalsFile: raw.TotalsFile}, nil
}
