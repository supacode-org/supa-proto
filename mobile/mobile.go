// Package mobile is the API the Android and iOS apps call through gomobile
// bind. It only uses types gomobile can export.
package mobile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"

	"supaproto/internal/router"
)

// Log levels passed to Platform.Log.
const (
	LogDebug = 0
	LogInfo  = 1
	LogWarn  = 2
	LogError = 3
)

// Notice kinds passed to Platform.Notice. Every kind must also be listed in
// the app notice spec before it is used.
const (
	NoticeBootstrapFailed = "bootstrap_failed"
	NoticeDoHUnavailable  = "doh_unavailable"
)

// Routing modes.
const (
	ModeGlobal  = string(router.ModeGlobal)
	ModeGeoIPCN = string(router.ModeGeoIPCN)
)

const (
	googleDoH = "https://dns.google/dns-query"
	aliDoH    = "https://dns.alidns.com/dns-query"
	// DefaultBootstrap resolves the proxy server and DoH host names.
	DefaultBootstrap = "8.8.8.8:53"
)

// Platform is implemented in Kotlin and Swift.
type Platform interface {
	// Protect keeps the socket outside the VPN tunnel. Android calls
	// VpnService.protect; iOS returns true.
	Protect(fd int) bool
	Log(level int, tag, message string)
	Notice(kind, message string)
}

var (
	platformMu sync.RWMutex
	platform   Platform
)

// SetPlatform installs the platform callbacks. Call it once at startup.
func SetPlatform(p Platform) {
	platformMu.Lock()
	defer platformMu.Unlock()
	platform = p
}

func current() Platform {
	platformMu.RLock()
	defer platformMu.RUnlock()
	return platform
}

func logf(level int, tag, message string) {
	if p := current(); p != nil {
		p.Log(level, tag, message)
	}
}

// DefaultDoH returns the DoH URL used when the user has not set one.
func DefaultDoH(mode string) string {
	if mode == ModeGeoIPCN {
		return aliDoH
	}
	return googleDoH
}

// ValidateRules checks imported rule text and reports the first error.
func ValidateRules(text string) error {
	_, err := router.ParseRules(text)
	return err
}

// protectedDialer returns a dialer whose sockets bypass the VPN tunnel:
// bound to the physical interface set by SetPhysicalInterface (iOS app), and
// passed to Platform.Protect (Android VpnService.protect).
func protectedDialer() *net.Dialer {
	return &net.Dialer{Control: func(network, _ string, c syscall.RawConn) error {
		var bindErr error
		protected := true
		p := current()
		if err := c.Control(func(fd uintptr) {
			if bindErr = bindPhysical(network, fd); bindErr != nil {
				return
			}
			if p != nil {
				protected = p.Protect(int(fd))
			}
		}); err != nil {
			return err
		}
		if bindErr != nil {
			return fmt.Errorf("bind to the physical interface: %w", bindErr)
		}
		if !protected {
			return errors.New("platform refused to protect socket")
		}
		return nil
	}}
}

func dialProtected(ctx context.Context, network, address string) (net.Conn, error) {
	return protectedDialer().DialContext(ctx, network, address)
}
