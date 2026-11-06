package mobile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"supaproto/internal/dns"
)

const (
	defaultPingTimeoutMillis = 3000
	defaultPingAttempts      = 3
)

// PingResult is the outcome of TCPPing. When every attempt fails, Successes
// is 0 and Error says why.
type PingResult struct {
	// Address is the IP:port that answered.
	Address       string
	ResolveMillis int64
	// ConnectMillis is the median TCP handshake time of successful attempts.
	ConnectMillis int64
	Successes     int
	Error         string
}

// TCPPing measures the TCP handshake time to host:port. Host names are
// resolved with the bootstrap DNS server, as real connections are. Sockets
// bypass the VPN tunnel. timeoutMillis and attempts fall back to 3000 and 3
// when not positive.
func TCPPing(bootstrap, host string, port, timeoutMillis, attempts int) *PingResult {
	if timeoutMillis <= 0 {
		timeoutMillis = defaultPingTimeoutMillis
	}
	if attempts <= 0 {
		attempts = defaultPingAttempts
	}
	timeout := time.Duration(timeoutMillis) * time.Millisecond
	result := &PingResult{}
	if port <= 0 || port > 65535 {
		result.Error = fmt.Sprintf("invalid port %d", port)
		return result
	}

	start := time.Now()
	addrs, err := resolveServer(bootstrap, host, timeout)
	result.ResolveMillis = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		logf(LogWarn, "TCPPing", result.Error)
		return result
	}

	var times []time.Duration
	var lastErr error
	for range attempts {
		elapsed, addr, err := connectOnce(addrs, port, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		times = append(times, elapsed)
		result.Address = addr
	}
	result.Successes = len(times)
	if len(times) == 0 {
		result.Error = lastErr.Error()
		logf(LogWarn, "TCPPing", fmt.Sprintf("%s:%d unreachable: %v", host, port, lastErr))
		return result
	}
	slices.Sort(times)
	result.ConnectMillis = times[len(times)/2].Milliseconds()
	return result
}

func resolveServer(bootstrap, host string, timeout time.Duration) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	if bootstrap == "" {
		bootstrap = DefaultBootstrap
	}
	b, err := dns.NewBootstrap(bootstrap, dialProtected)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return b.LookupIP(ctx, host)
}

// connectOnce tries each address in order and returns the first handshake.
func connectOnce(addrs []netip.Addr, port int, timeout time.Duration) (time.Duration, string, error) {
	var errs []error
	for _, addr := range addrs {
		target := net.JoinHostPort(addr.String(), strconv.Itoa(port))
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		start := time.Now()
		conn, err := protectedDialer().DialContext(ctx, "tcp", target)
		elapsed := time.Since(start)
		cancel()
		if err == nil {
			_ = conn.Close()
			return elapsed, target, nil
		}
		errs = append(errs, err)
	}
	return 0, "", errors.Join(errs...)
}
