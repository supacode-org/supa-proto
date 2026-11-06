// Package router decides whether a connection goes through the proxy,
// connects directly or is rejected.
package router

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"supaproto/internal/dns"
	"supaproto/internal/geoip"
)

type Mode string

const (
	ModeGlobal  Mode = "global"
	ModeGeoIPCN Mode = "geoip-cn"
)

// localPrefixes always connect directly. The rules carry no-resolve, so they
// never cause a DNS lookup for a domain-only target.
var localPrefixes = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // private
	"169.254.0.0/16", "127.0.0.0/8", // link-local, loopback
	"fc00::/7", "fe80::/10", "::1/128",
}

// ModeRules returns the built-in rule list for mode.
func ModeRules(mode Mode) ([]Rule, error) {
	var rules []Rule
	for _, s := range localPrefixes {
		p := netip.MustParsePrefix(s)
		kind := IPCIDR
		if p.Addr().Is6() {
			kind = IPCIDR6
		}
		rules = append(rules, Rule{Kind: kind, Prefix: p, Action: Direct, NoResolve: true})
	}
	switch mode {
	case ModeGlobal:
	case ModeGeoIPCN:
		rules = append(rules, Rule{Kind: GeoIP, Value: "CN", Action: Direct})
	default:
		return nil, fmt.Errorf("unknown routing mode %q", mode)
	}
	return append(rules, Rule{Kind: Final, Action: Proxy}), nil
}

// Resolver looks up addresses for rules that need an IP. It must return
// IPv4 addresses before IPv6 ones.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]netip.Addr, error)
}

// Target is a connection destination. Host is a domain name and may be
// empty; Addr is valid when the IP address is already known.
type Target struct {
	Host string
	Addr netip.Addr
}

type Decision struct {
	Action Action
	Rule   Rule
	// Addrs holds addresses resolved while matching, if any. A DIRECT
	// connection can reuse them instead of resolving again.
	Addrs []netip.Addr
	// ResolveErr is a lookup failure that made IP rules not match. Callers
	// log it; it is not a reason to fail the connection.
	ResolveErr error
}

type Router struct {
	rules    []Rule
	resolver Resolver
	country  func(netip.Addr) string
}

// New creates a router. resolver may be nil when the rules never need to
// resolve a domain.
func New(rules []Rule, resolver Resolver) (*Router, error) {
	if err := validate(rules); err != nil {
		return nil, err
	}
	return &Router{rules: rules, resolver: resolver, country: geoip.Country}, nil
}

func (r *Router) Route(ctx context.Context, t Target) Decision {
	host := dns.Normalize(t.Host)
	addr := t.Addr.Unmap()
	if ip, err := netip.ParseAddr(host); err == nil {
		addr, host = ip.Unmap(), ""
	}
	var d Decision
	resolved := false
	for _, rule := range r.rules {
		var ip netip.Addr
		if rule.Kind.needsIP() {
			ip = addr
			if !ip.IsValid() {
				if rule.NoResolve || host == "" {
					continue
				}
				if !resolved {
					resolved = true
					d.Addrs, d.ResolveErr = r.resolve(ctx, host)
				}
				ip = preferred(d.Addrs)
				if !ip.IsValid() {
					continue
				}
			}
		}
		if r.matches(rule, host, ip) {
			d.Action, d.Rule = rule.Action, rule
			return d
		}
	}
	// Unreachable: validate guarantees a trailing FINAL rule.
	d.Action = Proxy
	return d
}

func (r *Router) matches(rule Rule, host string, ip netip.Addr) bool {
	switch rule.Kind {
	case Domain:
		return host != "" && host == rule.Value
	case DomainSuffix:
		return host != "" && (host == rule.Value || strings.HasSuffix(host, "."+rule.Value))
	case DomainKeyword:
		return host != "" && strings.Contains(host, rule.Value)
	case IPCIDR, IPCIDR6:
		return rule.Prefix.Contains(ip)
	case GeoIP:
		return r.country(ip) == rule.Value
	case Final:
		return true
	}
	return false
}

func (r *Router) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if r.resolver == nil {
		return nil, fmt.Errorf("no resolver to look up %s", host)
	}
	return r.resolver.LookupIP(ctx, host)
}

// preferred picks the first IPv4 address, or the first IPv6 one if there is
// no IPv4 address.
func preferred(addrs []netip.Addr) netip.Addr {
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a.Unmap()
		}
	}
	if len(addrs) > 0 {
		return addrs[0]
	}
	return netip.Addr{}
}
