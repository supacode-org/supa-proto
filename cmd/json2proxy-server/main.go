package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"json2proxy/internal/config"
	"json2proxy/internal/flow"
	"json2proxy/internal/logging"
	"json2proxy/internal/obfs"
	"json2proxy/internal/protocol"
	"json2proxy/internal/socks5"
)

const (
	errorAuthentication  = "authentication_failed"
	errorConnectionLimit = "connection_limit_exceeded"
	errorPrivateTarget   = "private_target_denied"
	errorInvalidRequest  = "invalid_request"
)

var errPrivateTarget = errors.New("private target denied")

type contextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type app struct {
	cfg              config.Server
	users            map[string]config.User
	replay           map[string]time.Time
	active           map[string]int
	coverActive      map[string]int
	coverRate        map[string]int
	coverTotal       int
	udpActive        map[string]int
	udpRates         map[string]*udpRateState
	udpDNS           map[string]udpDNSCacheEntry
	handshakeTimeout time.Duration
	dialTimeout      time.Duration
	idleTimeout      time.Duration
	mu               sync.Mutex
	log              *logging.Logger
	provider         *obfs.Provider
	outbound         contextDialer
	outboundSOCKS    *socks5.Client
	// udpLookup replaces the system resolver for UDP domain targets in tests.
	udpLookup func(ctx context.Context, host string) ([]net.IP, error)
}

type frameWriter struct {
	wire    *obfs.Conn
	session *protocol.Session
	timeout time.Duration
	mu      sync.Mutex
}

func (w *frameWriter) envelope(env protocol.Envelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeEnvelopeLocked(env)
}

func (w *frameWriter) writeEnvelopeLocked(env protocol.Envelope) error {
	_ = w.wire.SetWriteDeadline(time.Now().Add(w.timeout))
	err := w.wire.WriteEnvelope(env)
	_ = w.wire.SetWriteDeadline(time.Time{})
	return err
}

func (w *frameWriter) frame(frame protocol.Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	env, err := w.session.Seal(frame)
	if err != nil {
		return err
	}
	return w.writeEnvelopeLocked(env)
}

func (w *frameWriter) presetError(code, message string) error {
	return w.envelope(protocol.Envelope{
		V: protocol.Version, Kind: "error", Code: code, Message: message,
	})
}

func writePresetError(wire *obfs.Conn, code, message string) {
	writer := &frameWriter{wire: wire, timeout: 3 * time.Second}
	_ = writer.presetError(code, message)
}

func main() {
	configPath := flag.String("config", ".private/configs/server.json", "server config")
	flag.Parse()

	var cfg config.Server
	must(config.Load(*configPath, &cfg))
	cfg.Defaults()
	must(cfg.Validate())
	provider, err := obfs.Load(cfg.Obfs)
	must(err)

	logger, err := logging.New(cfg.LogFile, cfg.LogSize)
	must(err)
	defer logger.Close()

	a := &app{
		cfg: cfg, users: make(map[string]config.User), replay: make(map[string]time.Time),
		active: make(map[string]int), coverActive: make(map[string]int), coverRate: make(map[string]int),
		udpActive: make(map[string]int),
		udpRates:  make(map[string]*udpRateState), udpDNS: make(map[string]udpDNSCacheEntry),
		log: logger, provider: provider,
	}
	a.handshakeTimeout, _ = config.Duration(cfg.HandshakeTimeout)
	a.dialTimeout, _ = config.Duration(cfg.DialTimeout)
	a.idleTimeout, _ = config.Duration(cfg.IdleTimeout)
	if cfg.Outbound.Enabled() {
		a.outboundSOCKS = socks5.New(cfg.Outbound.Address, cfg.Outbound.Username, cfg.Outbound.Password)
		a.outbound = a.outboundSOCKS
	}
	for _, user := range cfg.Users {
		a.users[user.ClientID] = user
	}

	listener, err := net.Listen("tcp", cfg.Listen)
	must(err)
	defer listener.Close()
	logger.Printf("server starting product=JSON2Proxy protocol=v%d listen=%s log_size=%s performance=%s obfs=%s obfs_version=%s participation=%.3f obfs_hash=%s egress=%s egress_proxy=%s udp=%t",
		protocol.Version, cfg.Listen, cfg.LogSize, cfg.Performance.Mode, provider.ID(),
		provider.Descriptor.Version, provider.Participation, provider.Hash(), a.egressType(), a.egressProxy(), cfg.UDP.IsEnabled())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.Printf("accept failed error=%q", err)
			continue
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(true)
			_ = tcp.SetKeepAlivePeriod(60 * time.Second)
			_ = tcp.SetNoDelay(true)
		}
		go a.serve(conn)
	}
}

func (a *app) serve(tunnel net.Conn) {
	defer tunnel.Close()
	started := time.Now()
	source := tunnel.RemoteAddr().String()
	wire, err := a.provider.NewConn(tunnel, "s2c")
	if err != nil {
		return
	}
	defer wire.Stop()
	_ = tunnel.SetDeadline(time.Now().Add(a.handshakeTimeout))
	wire.EmitPreHello()
	wire.SetPhase("handshake")

	clientHello, err := wire.ReadEnvelope()
	if err != nil || clientHello.Kind != "client_hello" {
		a.log.Printf("connection rejected source=%s reason=invalid_hello", source)
		writePresetError(wire, errorInvalidRequest, "invalid request")
		return
	}
	user, ok := a.users[clientHello.ClientID]
	if !ok || user.Status != "enabled" {
		a.log.Printf("connection rejected source=%s reason=authentication_failed", source)
		writePresetError(wire, errorAuthentication, "authentication failed")
		return
	}
	root, err := config.Secret(user.Secret)
	if err != nil || protocol.ValidateClientHello(
		clientHello, root, a.provider.Identity(), time.Now(), 60*time.Second) != nil {
		a.log.Printf("connection rejected source=%s user=%s reason=authentication_failed", source, clientHello.ClientID)
		writePresetError(wire, errorAuthentication, "authentication failed")
		return
	}
	release, rejection := a.claim(clientHello.ClientID, clientHello.CN, user.MaxConnections)
	if rejection == "replay" {
		a.log.Printf("connection rejected source=%s user=%s reason=replay", source, clientHello.ClientID)
		writePresetError(wire, errorAuthentication, "authentication failed")
		return
	}
	if rejection == "quota" {
		a.log.Printf("connection rejected source=%s user=%s reason=connection_limit active=%d limit=%d",
			source, clientHello.ClientID, a.activeCount(clientHello.ClientID), user.MaxConnections)
		writePresetError(wire, errorConnectionLimit, "connection limit exceeded")
		return
	}
	defer release()

	serverHello, serverPrivate, err := protocol.NewServerHello(
		root, a.provider.Identity(), clientHello, time.Now(), 24*time.Hour)
	if err != nil {
		return
	}
	writer := &frameWriter{wire: wire, timeout: 30 * time.Second}
	if err = writer.envelope(serverHello); err != nil {
		return
	}
	session, err := protocol.NewSession(
		root, a.provider.Identity(), clientHello, serverHello, serverPrivate, "s2c")
	if err != nil {
		return
	}
	writer.session = session

	frame, err := readFrame(wire, session)
	if err != nil || frame.Type != protocol.FrameKeyConfirm ||
		!session.VerifyConfirmation("client", frame.Payload) {
		a.log.Printf("connection rejected source=%s user=%s session=%s reason=key_confirmation", source, clientHello.ClientID, session.ID())
		_ = writer.presetError(errorInvalidRequest, "invalid request")
		return
	}
	if err = writer.frame(protocol.Frame{Type: protocol.FrameKeyConfirm, Payload: session.Confirmation("server")}); err != nil {
		return
	}
	wire.SetPhase("established")
	wire.StartScheduler()
	frame, err = readFrame(wire, session)
	if err != nil {
		a.log.Printf("connection rejected source=%s user=%s session=%s reason=invalid_open", source, clientHello.ClientID, session.ID())
		_ = writer.presetError(errorInvalidRequest, "invalid request")
		return
	}
	if frame.Type == protocol.FrameUDPOpen {
		if !a.cfg.UDP.IsEnabled() {
			_ = writer.frame(protocol.Frame{Type: protocol.FrameUDPOpenResult, Payload: protocol.Result(4, "UDP disabled")})
			a.log.Printf("connection rejected source=%s user=%s session=%s udp=true reason=udp_disabled", source, clientHello.ClientID, session.ID())
			return
		}
		a.serveUDP(tunnel, writer, session, user, source, started)
		return
	}
	if frame.Type == protocol.FrameCoverOpen {
		a.serveCover(tunnel, wire, writer, session, clientHello.ClientID, source, started, frame.Payload)
		return
	}
	if frame.Type != protocol.FrameOpen {
		a.log.Printf("connection rejected source=%s user=%s session=%s reason=invalid_open", source, clientHello.ClientID, session.ID())
		_ = writer.presetError(errorInvalidRequest, "invalid request")
		return
	}
	host, port, err := protocol.ParseOpen(frame.Payload)
	if err != nil {
		_ = writer.presetError(errorInvalidRequest, "invalid request")
		return
	}
	requestedTarget := net.JoinHostPort(host, strconv.Itoa(int(port)))
	target, resolvedTarget, err := a.dial(host, port, user.AllowPrivate)
	if err != nil {
		if errors.Is(err, errPrivateTarget) {
			a.log.Printf("connection rejected source=%s target=%s user=%s session=%s egress=%s egress_proxy=%s reason=private_target_denied",
				source, requestedTarget, clientHello.ClientID, session.ID(), a.egressType(), a.egressProxy())
			_ = writer.presetError(errorPrivateTarget, "private target denied")
			return
		}
		a.log.Printf("connection failed source=%s target=%s user=%s session=%s egress=%s egress_proxy=%s reason=target_connect error=%q",
			source, requestedTarget, clientHello.ClientID, session.ID(), a.egressType(), a.egressProxy(), err)
		_ = writer.frame(protocol.Frame{Type: protocol.FrameOpenResult, Payload: protocol.Result(3, "connect failed")})
		return
	}
	defer target.Close()
	if tcp, ok := target.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetNoDelay(true)
	}
	if err = writer.frame(protocol.Frame{Type: protocol.FrameOpenResult, Payload: protocol.Result(0, "")}); err != nil {
		return
	}
	_ = tunnel.SetDeadline(time.Time{})
	a.log.Printf("connection established source=%s target=%s requested_target=%s user=%s session=%s egress=%s egress_proxy=%s",
		source, resolvedTarget, requestedTarget, clientHello.ClientID, session.ID(), a.egressType(), a.egressProxy())

	var bytesUp, bytesDown atomic.Uint64
	var rejecting atomic.Bool
	downDone := make(chan error, 1)
	go func() {
		adaptive := flow.NewAdaptiveReader(flow.Mode(a.cfg.Performance.Mode))
		for {
			_ = target.SetReadDeadline(time.Now().Add(a.idleTimeout))
			payload, readErr := adaptive.ReadFrame(target)
			if len(payload) != 0 {
				if rejecting.Load() {
					flow.ReleaseFrame(payload)
					downDone <- nil
					return
				}
				started := time.Now()
				sendErr := writer.frame(protocol.Frame{Type: protocol.FrameData, Payload: payload})
				adaptive.Observe(len(payload), time.Since(started), sendErr)
				bytesDown.Add(uint64(len(payload)))
				flow.ReleaseFrame(payload)
				if sendErr != nil {
					downDone <- sendErr
					return
				}
			}
			if readErr != nil {
				if !rejecting.Load() {
					_ = writer.frame(protocol.Frame{Type: protocol.FrameCloseWrite})
				}
				closeWrite(tunnel)
				if flow.IsNormalClose(readErr) {
					readErr = nil
				}
				downDone <- readErr
				return
			}
		}
	}()

	reason := "completed"
	clientClosed := false
	for {
		_ = tunnel.SetReadDeadline(time.Now().Add(a.idleTimeout))
		frame, err = readFrame(wire, session)
		if err != nil {
			if !clientClosed && !flow.IsNormalClose(err) {
				reason = "invalid_request"
				rejecting.Store(true)
				_ = writer.presetError(errorInvalidRequest, "invalid request")
				_ = target.Close()
			}
			closeWrite(target)
			break
		}
		switch frame.Type {
		case protocol.FrameData:
			if clientClosed {
				reason = "invalid_request"
				rejecting.Store(true)
				_ = writer.presetError(errorInvalidRequest, "invalid request")
				_ = target.Close()
				break
			}
			if err = writeConn(target, frame.Payload, 30*time.Second); err != nil {
				reason = "target_write_failed"
				_ = target.Close()
				break
			}
			bytesUp.Add(uint64(len(frame.Payload)))
		case protocol.FrameCloseWrite:
			clientClosed = true
			closeWrite(target)
		case protocol.FramePing:
			if err = writer.frame(protocol.Frame{Type: protocol.FramePong, Payload: frame.Payload}); err != nil {
				reason = "tunnel_write_failed"
				_ = target.Close()
				break
			}
		case protocol.FrameReset:
			reason = "client_reset"
			_ = target.Close()
		default:
			reason = "invalid_request"
			rejecting.Store(true)
			_ = writer.presetError(errorInvalidRequest, "invalid request")
			_ = target.Close()
		}
		if reason != "completed" {
			break
		}
	}
	downErr := <-downDone
	if downErr != nil && reason == "completed" {
		reason = "target_read_failed"
	}
	a.log.Printf("connection closed source=%s target=%s user=%s session=%s bytes_up=%d bytes_down=%d duration=%s egress=%s egress_proxy=%s reason=%s",
		source, resolvedTarget, clientHello.ClientID, session.ID(), bytesUp.Load(), bytesDown.Load(),
		time.Since(started).Round(time.Millisecond), a.egressType(), a.egressProxy(), reason)
}

func readFrame(wire *obfs.Conn, session *protocol.Session) (protocol.Frame, error) {
	env, err := wire.ReadEnvelope()
	if err != nil {
		return protocol.Frame{}, err
	}
	return session.Open(env)
}

func writeConn(conn net.Conn, b []byte, timeout time.Duration) error {
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	defer conn.SetWriteDeadline(time.Time{})
	for len(b) != 0 {
		n, err := conn.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func (a *app) claim(clientID, nonce string, max int) (func(), string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, expiry := range a.replay {
		if now.After(expiry) {
			delete(a.replay, key)
		}
	}
	key := clientID + ":" + nonce
	if _, exists := a.replay[key]; exists {
		return nil, "replay"
	}
	if a.active[clientID] >= max {
		return nil, "quota"
	}
	a.replay[key] = now.Add(2 * time.Minute)
	a.active[clientID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.active[clientID]--
			a.mu.Unlock()
		})
	}, ""
}

func (a *app) activeCount(clientID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active[clientID]
}

func (a *app) dial(host string, port uint16, allowPrivate bool) (net.Conn, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.dialTimeout)
	defer cancel()
	requestedTarget := net.JoinHostPort(host, strconv.Itoa(int(port)))
	if a.outbound != nil && a.cfg.Outbound.UsesRemoteDNS() && net.ParseIP(host) == nil {
		conn, err := a.outbound.DialContext(ctx, "tcp", requestedTarget)
		if err != nil {
			return nil, "", err
		}
		return conn, requestedTarget, nil
	}
	var addresses []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addresses = []net.IP{ip}
	} else {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, "", err
		}
		addresses = ips
	}
	dialer := net.Dialer{Timeout: a.dialTimeout, KeepAlive: 60 * time.Second}
	allowed := false
	var lastErr error
	for _, ip := range addresses {
		if !allowPrivate && forbidden(ip) {
			continue
		}
		allowed = true
		resolvedTarget := net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))
		var conn net.Conn
		var err error
		if a.outbound != nil {
			conn, err = a.outbound.DialContext(ctx, "tcp", resolvedTarget)
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", resolvedTarget)
		}
		if err == nil {
			return conn, resolvedTarget, nil
		}
		lastErr = err
	}
	if !allowed {
		return nil, "", errPrivateTarget
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", errors.New("target unreachable")
}

func (a *app) egressType() string {
	if a.outbound != nil {
		return "socks5"
	}
	return "direct"
}

func (a *app) egressProxy() string {
	if a.outbound != nil {
		return a.cfg.Outbound.Address
	}
	return "-"
}

func forbidden(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() ||
		addr.IsUnspecified() {
		return true
	}
	for _, prefix := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"240.0.0.0/4", "100::/64", "2001:db8::/32",
	} {
		p := netip.MustParsePrefix(prefix)
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func closeWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	} else {
		_ = conn.Close()
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

var _ = io.EOF
