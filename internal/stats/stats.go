// Package stats counts the traffic of a running engine: active connections,
// bytes per direction and per host, recent DNS results and routing decisions.
// Byte counts are taken at the app side of each flow, so they exclude protocol
// framing, encryption and obfuscation overhead.
package stats

import (
	"cmp"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultMaxHosts = 512
	DefaultMaxDNS   = 200
	DefaultMaxFlows = 200
	NetworkTCP      = "tcp"
	NetworkUDP      = "udp"
)

type Collector struct {
	now      func() time.Time
	maxHosts int
	maxDNS   int

	tcp, udp atomic.Int64
	up, down atomic.Int64

	// Cover traffic is counted apart from real flows: cover is the active cover
	// connection count, coverUp/coverDown its payload bytes per direction.
	cover              atomic.Int64
	coverUp, coverDown atomic.Int64

	mu    sync.Mutex
	hosts map[string]*host
	dns   map[string]DNSRecord
	flows []FlowRecord // ring buffer
	next  int          // next slot in flows
	full  bool
}

type host struct {
	name     string
	up, down atomic.Int64
	tcp, udp atomic.Int64
	last     atomic.Int64 // unix milliseconds
	// Guarded by Collector.mu.
	action, rule string
}

func (h *host) active() bool { return h.tcp.Load() > 0 || h.udp.Load() > 0 }

// New returns a collector with the default limits.
func New() *Collector {
	return NewWithLimits(DefaultMaxHosts, DefaultMaxDNS, DefaultMaxFlows, time.Now)
}

// NewWithLimits sets how many hosts, DNS results and flow records are kept.
func NewWithLimits(maxHosts, maxDNS, maxFlows int, now func() time.Time) *Collector {
	return &Collector{
		now:      now,
		maxHosts: maxHosts,
		maxDNS:   maxDNS,
		hosts:    make(map[string]*host),
		dns:      make(map[string]DNSRecord),
		flows:    make([]FlowRecord, maxFlows),
	}
}

// Flow tracks one TCP connection or UDP flow from routing to its end. Call
// Fail if it never starts, or Start and later Close.
type Flow struct {
	c       *Collector
	h       *host
	record  FlowRecord
	started bool
	closed  atomic.Bool
}

// Route records the routing decision for a new flow to hostKey:port.
func (c *Collector) Route(network, hostKey string, port uint16, action, rule string) *Flow {
	now := c.now().UnixMilli()
	c.mu.Lock()
	h := c.hosts[hostKey]
	if h == nil {
		c.evictHostLocked()
		h = &host{name: hostKey}
		c.hosts[hostKey] = h
	}
	h.action, h.rule = action, rule
	c.mu.Unlock()
	h.last.Store(now)
	return &Flow{c: c, h: h, record: FlowRecord{
		Time: now, Network: network, Host: hostKey, Port: port, Action: action, Rule: rule,
	}}
}

// evictHostLocked makes room for one more host by dropping the idle host
// that has been quiet the longest. Hosts with open flows are kept even when
// that exceeds the limit.
func (c *Collector) evictHostLocked() {
	if len(c.hosts) < c.maxHosts {
		return
	}
	var oldest *host
	for _, h := range c.hosts {
		if h.active() {
			continue
		}
		if oldest == nil || h.last.Load() < oldest.last.Load() {
			oldest = h
		}
	}
	if oldest != nil {
		delete(c.hosts, oldest.name)
	}
}

// Start counts the flow as active and records it.
func (f *Flow) Start() {
	f.started = true
	f.c.addFlow(f.record)
	if f.record.Network == NetworkTCP {
		f.c.tcp.Add(1)
		f.h.tcp.Add(1)
	} else {
		f.c.udp.Add(1)
		f.h.udp.Add(1)
	}
}

// Fail records a flow that was rejected or could not be set up; err is nil
// for a REJECT decision.
func (f *Flow) Fail(err error) {
	if err != nil {
		f.record.Error = err.Error()
	}
	f.c.addFlow(f.record)
}

// Close ends a started flow. It is safe to call more than once.
func (f *Flow) Close() {
	if !f.started || !f.closed.CompareAndSwap(false, true) {
		return
	}
	if f.record.Network == NetworkTCP {
		f.c.tcp.Add(-1)
		f.h.tcp.Add(-1)
	} else {
		f.c.udp.Add(-1)
		f.h.udp.Add(-1)
	}
	f.h.last.Store(f.c.now().UnixMilli())
}

// AddUp counts bytes sent by the app.
func (f *Flow) AddUp(n int) {
	if n > 0 {
		f.c.up.Add(int64(n))
		f.h.up.Add(int64(n))
		f.h.last.Store(f.c.now().UnixMilli())
	}
}

// AddDown counts bytes received by the app.
func (f *Flow) AddDown(n int) {
	if n > 0 {
		f.c.down.Add(int64(n))
		f.h.down.Add(int64(n))
		f.h.last.Store(f.c.now().UnixMilli())
	}
}

// Wrap counts traffic on the app side of a flow: reads are uploads, writes
// are downloads.
func (f *Flow) Wrap(conn net.Conn) net.Conn { return &countingConn{Conn: conn, flow: f} }

type countingConn struct {
	net.Conn
	flow *Flow
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.flow.AddUp(n)
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.flow.AddDown(n)
	return n, err
}

// CloseWrite keeps half-close working through the wrapper; connections
// without it are closed, as client.CloseWrite does.
func (c *countingConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

// AddCoverConn adjusts the active cover-connection count by delta.
func (c *Collector) AddCoverConn(delta int) { c.cover.Add(int64(delta)) }

// AddCoverBytes counts cover payload bytes sent (up) and received (down).
func (c *Collector) AddCoverBytes(up, down int) {
	if up > 0 {
		c.coverUp.Add(int64(up))
	}
	if down > 0 {
		c.coverDown.Add(int64(down))
	}
}

func (c *Collector) addFlow(r FlowRecord) {
	if len(c.flows) == 0 {
		return
	}
	c.mu.Lock()
	c.flows[c.next] = r
	c.next = (c.next + 1) % len(c.flows)
	if c.next == 0 {
		c.full = true
	}
	c.mu.Unlock()
}

// RecordDNS keeps the latest DoH result for name.
func (c *Collector) RecordDNS(name string, addrs []netip.Addr, err error) {
	if c.maxDNS <= 0 {
		return
	}
	r := DNSRecord{Name: name, Time: c.now().UnixMilli(), Addrs: make([]string, len(addrs))}
	for i, a := range addrs {
		r.Addrs[i] = a.String()
	}
	if err != nil {
		r.Error = err.Error()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.dns[name]; !ok && len(c.dns) >= c.maxDNS {
		oldest := ""
		for n, rec := range c.dns {
			if oldest == "" || rec.Time < c.dns[oldest].Time {
				oldest = n
			}
		}
		delete(c.dns, oldest)
	}
	c.dns[name] = r
}

type Snapshot struct {
	Time      int64          `json:"time"`
	TCP       int64          `json:"tcp"`
	UDP       int64          `json:"udp"`
	Up        int64          `json:"up"`
	Down      int64          `json:"down"`
	Cover     int64          `json:"cover"`
	CoverUp   int64          `json:"cover_up"`
	CoverDown int64          `json:"cover_down"`
	Hosts     []HostSnapshot `json:"hosts,omitempty"`
	DNS       []DNSRecord    `json:"dns,omitempty"`
	Flows     []FlowRecord   `json:"flows,omitempty"`
}

type HostSnapshot struct {
	Host   string `json:"host"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
	TCP    int64  `json:"tcp"`
	UDP    int64  `json:"udp"`
	Action string `json:"action"`
	Rule   string `json:"rule"`
	Last   int64  `json:"last"`
}

type DNSRecord struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	Error string   `json:"error"`
	Time  int64    `json:"time"`
}

type FlowRecord struct {
	Time    int64  `json:"time"`
	Network string `json:"network"`
	Host    string `json:"host"`
	Port    uint16 `json:"port"`
	Action  string `json:"action"`
	Rule    string `json:"rule"`
	Error   string `json:"error"`
}

// Snapshot returns the current counters; with detail it also lists hosts
// (most recently active first), DNS results and flows (newest first).
func (c *Collector) Snapshot(detail bool) Snapshot {
	s := Snapshot{
		Time: c.now().UnixMilli(),
		TCP:  c.tcp.Load(), UDP: c.udp.Load(),
		Up: c.up.Load(), Down: c.down.Load(),
		Cover: c.cover.Load(), CoverUp: c.coverUp.Load(), CoverDown: c.coverDown.Load(),
	}
	if !detail {
		return s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s.Hosts = make([]HostSnapshot, 0, len(c.hosts))
	for _, h := range c.hosts {
		s.Hosts = append(s.Hosts, HostSnapshot{
			Host: h.name, Up: h.up.Load(), Down: h.down.Load(), TCP: h.tcp.Load(), UDP: h.udp.Load(),
			Action: h.action, Rule: h.rule, Last: h.last.Load(),
		})
	}
	slices.SortFunc(s.Hosts, func(a, b HostSnapshot) int { return cmp.Compare(b.Last, a.Last) })
	s.DNS = make([]DNSRecord, 0, len(c.dns))
	for _, r := range c.dns {
		s.DNS = append(s.DNS, r)
	}
	slices.SortFunc(s.DNS, func(a, b DNSRecord) int { return cmp.Compare(b.Time, a.Time) })
	count := c.next
	if c.full {
		count = len(c.flows)
	}
	s.Flows = make([]FlowRecord, 0, count)
	for i := 1; i <= count; i++ {
		s.Flows = append(s.Flows, c.flows[(c.next-i+len(c.flows))%len(c.flows)])
	}
	return s
}
