package router

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"supaproto/internal/dns"
	"supaproto/internal/geoip"
)

type Action uint8

const (
	Proxy Action = iota
	Direct
	Reject
)

func (a Action) String() string {
	switch a {
	case Proxy:
		return "PROXY"
	case Direct:
		return "DIRECT"
	case Reject:
		return "REJECT"
	}
	return fmt.Sprintf("Action(%d)", a)
}

type Kind uint8

const (
	Domain Kind = iota
	DomainSuffix
	DomainKeyword
	IPCIDR
	IPCIDR6
	GeoIP
	Final
)

var kindNames = map[Kind]string{
	Domain:        "DOMAIN",
	DomainSuffix:  "DOMAIN-SUFFIX",
	DomainKeyword: "DOMAIN-KEYWORD",
	IPCIDR:        "IP-CIDR",
	IPCIDR6:       "IP-CIDR6",
	GeoIP:         "GEOIP",
	Final:         "FINAL",
}

func (k Kind) String() string { return kindNames[k] }

// needsIP reports whether the rule matches on the target's IP address.
func (k Kind) needsIP() bool { return k == IPCIDR || k == IPCIDR6 || k == GeoIP }

type Rule struct {
	Kind   Kind
	Value  string // domain, keyword or country code
	Prefix netip.Prefix
	Action Action
	// NoResolve skips the rule for domain-only targets instead of resolving.
	NoResolve bool
}

func (r Rule) String() string {
	parts := []string{r.Kind.String()}
	switch {
	case r.Kind == IPCIDR || r.Kind == IPCIDR6:
		parts = append(parts, r.Prefix.String())
	case r.Kind != Final:
		parts = append(parts, r.Value)
	}
	parts = append(parts, r.Action.String())
	if r.NoResolve {
		parts = append(parts, "no-resolve")
	}
	return strings.Join(parts, ",")
}

// ParseRule parses one rule such as "DOMAIN-SUFFIX,google.com,PROXY".
func ParseRule(line string) (Rule, error) {
	fields := strings.Split(line, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	var kind Kind
	found := false
	for k, name := range kindNames {
		if strings.EqualFold(fields[0], name) {
			kind, found = k, true
			break
		}
	}
	if !found {
		return Rule{}, fmt.Errorf("unknown rule type %q", fields[0])
	}
	want := 3
	if kind == Final {
		want = 2
	}
	if kind.needsIP() && len(fields) == 4 {
		if !strings.EqualFold(fields[3], "no-resolve") {
			return Rule{}, fmt.Errorf("unknown option %q", fields[3])
		}
	} else if len(fields) != want {
		return Rule{}, fmt.Errorf("%s takes %d fields, got %d", kind, want, len(fields))
	}
	r := Rule{Kind: kind, NoResolve: len(fields) == 4}
	action, err := parseAction(fields[want-1])
	if err != nil {
		return Rule{}, err
	}
	r.Action = action
	if kind == Final {
		return r, nil
	}
	value := fields[1]
	switch kind {
	case Domain, DomainSuffix, DomainKeyword:
		r.Value = dns.Normalize(value)
		if r.Value == "" || strings.ContainsAny(r.Value, " \t/") {
			return Rule{}, fmt.Errorf("invalid domain %q", value)
		}
	case IPCIDR, IPCIDR6:
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return Rule{}, fmt.Errorf("invalid CIDR %q", value)
		}
		if p.Addr().Is4() != (kind == IPCIDR) {
			return Rule{}, fmt.Errorf("%s does not accept %s", kind, value)
		}
		r.Prefix = p.Masked()
	case GeoIP:
		if !geoip.Supported(value) {
			return Rule{}, fmt.Errorf("GEOIP only supports CN, got %q", value)
		}
		r.Value = strings.ToUpper(value)
	}
	return r, nil
}

// ParseRules parses one rule per line, ignoring blank lines and lines that
// start with '#'. The list must end with exactly one FINAL rule.
func ParseRules(text string) ([]Rule, error) {
	var rules []Rule
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := ParseRule(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		rules = append(rules, r)
	}
	return rules, validate(rules)
}

func validate(rules []Rule) error {
	if len(rules) == 0 || rules[len(rules)-1].Kind != Final {
		return errors.New("rules must end with a FINAL rule")
	}
	for _, r := range rules[:len(rules)-1] {
		if r.Kind == Final {
			return errors.New("FINAL must be the last rule")
		}
	}
	return nil
}

func parseAction(s string) (Action, error) {
	for _, a := range []Action{Proxy, Direct, Reject} {
		if strings.EqualFold(s, a.String()) {
			return a, nil
		}
	}
	return 0, fmt.Errorf("unknown action %q", s)
}
