// Package fakeip hands out placeholder IPv4 addresses for domain names so
// that connections made by apps can be mapped back to the name they looked up.
package fakeip

import (
	"container/list"
	"fmt"
	"net/netip"
	"sync"

	"supaproto/internal/dns"
)

var DefaultPrefix = netip.MustParsePrefix("198.18.0.0/15")

const DefaultCapacity = 65536

type entry struct {
	name string
	ip   netip.Addr
}

// Pool maps names to addresses in a prefix. When full, the least recently
// used mapping is evicted and its address reused.
type Pool struct {
	mu       sync.Mutex
	prefix   netip.Prefix
	first    netip.Addr
	next     netip.Addr
	capacity int
	byName   map[string]*list.Element
	byIP     map[netip.Addr]*list.Element
	lru      *list.List // front is most recently used
}

func NewPool(prefix netip.Prefix, capacity int) (*Pool, error) {
	prefix = prefix.Masked()
	if !prefix.Addr().Is4() || prefix.Bits() > 30 {
		return nil, fmt.Errorf("fake-IP prefix must be IPv4 and at most /30, got %s", prefix)
	}
	// Exclude the network and broadcast addresses.
	usable := 1<<(32-prefix.Bits()) - 2
	if capacity <= 0 || capacity > usable {
		capacity = usable
	}
	p := &Pool{prefix: prefix, first: prefix.Addr().Next(), capacity: capacity}
	p.Reset()
	return p, nil
}

// Lookup returns the address for name, allocating one if needed.
func (p *Pool) Lookup(name string) netip.Addr {
	name = dns.Normalize(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.byName[name]; ok {
		p.lru.MoveToFront(e)
		return e.Value.(*entry).ip
	}
	var ip netip.Addr
	if p.lru.Len() >= p.capacity {
		oldest := p.lru.Back()
		old := oldest.Value.(*entry)
		p.lru.Remove(oldest)
		delete(p.byName, old.name)
		delete(p.byIP, old.ip)
		ip = old.ip
	} else {
		// Until the pool is full nothing is evicted, so addresses are handed
		// out in order and never collide.
		ip = p.next
		p.next = p.next.Next()
	}
	e := p.lru.PushFront(&entry{name: name, ip: ip})
	p.byName[name] = e
	p.byIP[ip] = e
	return ip
}

// Name returns the domain mapped to ip and marks the mapping as used.
func (p *Pool) Name(ip netip.Addr) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byIP[ip.Unmap()]
	if !ok {
		return "", false
	}
	p.lru.MoveToFront(e)
	return e.Value.(*entry).name, true
}

// Contains reports whether ip is inside the fake-IP prefix.
func (p *Pool) Contains(ip netip.Addr) bool { return p.prefix.Contains(ip.Unmap()) }

// Reset drops every mapping, for example when the VPN restarts.
func (p *Pool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = p.first
	p.byName = make(map[string]*list.Element)
	p.byIP = make(map[netip.Addr]*list.Element)
	p.lru = list.New()
}
