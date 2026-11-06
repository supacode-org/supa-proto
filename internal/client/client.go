// Package client opens SupaProto sessions to a server. Every TCP stream and
// every UDP association uses its own authenticated session.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"supaproto/internal/config"
	"supaproto/internal/obfs"
	"supaproto/internal/protocol"
)

const writeTimeout = 30 * time.Second

// Options lets platform code control how the server is reached.
type Options struct {
	// Dial connects to the server. Nil uses net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Resolve looks up the server host name, for example through bootstrap
	// DNS. Nil leaves name resolution to Dial.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

type Client struct {
	cfg              config.Client
	root             []byte
	provider         *obfs.Provider
	connectTimeout   time.Duration
	handshakeTimeout time.Duration
	idleTimeout      time.Duration
	udpIdleTimeout   time.Duration
	dial             func(ctx context.Context, network, address string) (net.Conn, error)
	resolve          func(ctx context.Context, host string) ([]netip.Addr, error)
}

// New validates cfg (after applying defaults) and prepares a client.
func New(cfg config.Client, opts Options) (*Client, error) {
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	root, err := config.Secret(cfg.Secret)
	if err != nil {
		return nil, err
	}
	provider, err := obfs.Load(cfg.Obfs)
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, root: root, provider: provider, dial: opts.Dial, resolve: opts.Resolve}
	c.connectTimeout, _ = config.Duration(cfg.ConnectTimeout)
	c.handshakeTimeout, _ = config.Duration(cfg.HandshakeTimeout)
	c.idleTimeout, _ = config.Duration(cfg.IdleTimeout)
	c.udpIdleTimeout, _ = config.Duration(cfg.UDP.IdleTimeout)
	if c.dial == nil {
		c.dial = (&net.Dialer{KeepAlive: 60 * time.Second}).DialContext
	}
	return c, nil
}

// Config returns the configuration with defaults applied.
func (c *Client) Config() config.Client         { return c.cfg }
func (c *Client) Provider() *obfs.Provider      { return c.provider }
func (c *Client) UDPIdleTimeout() time.Duration { return c.udpIdleTimeout }

// Probe connects and authenticates once, then closes the session. It checks
// that the server is reachable and accepts the credentials.
func (c *Client) Probe(ctx context.Context) error {
	s, err := c.connect(ctx)
	if err != nil {
		return err
	}
	s.close()
	return nil
}

// OpenError reports that the server refused to open the target.
type OpenError struct {
	Code    byte
	Message string
}

func (e *OpenError) Error() string { return fmt.Sprintf("target refused: %s", e.Message) }

// session is one authenticated, established SupaProto connection.
type session struct {
	tunnel net.Conn
	wire   *obfs.Conn
	crypto *protocol.Session
	mu     sync.Mutex // serializes envelope writes
}

func (s *session) ID() string {
	if s.crypto == nil {
		return ""
	}
	return s.crypto.ID()
}

func (s *session) envelope(env protocol.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeEnvelopeLocked(env)
}

func (s *session) writeEnvelopeLocked(env protocol.Envelope) error {
	_ = s.wire.SetWriteDeadline(time.Now().Add(writeTimeout))
	err := s.wire.WriteEnvelope(env)
	_ = s.wire.SetWriteDeadline(time.Time{})
	return err
}

func (s *session) frame(frame protocol.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	env, err := s.crypto.Seal(frame)
	if err != nil {
		return err
	}
	return s.writeEnvelopeLocked(env)
}

func (s *session) readFrame() (protocol.Frame, error) {
	env, err := s.wire.ReadEnvelope()
	if err != nil {
		return protocol.Frame{}, err
	}
	if env.Kind == "error" {
		return protocol.Frame{}, remoteError(env)
	}
	return s.crypto.Open(env)
}

func (s *session) close() {
	s.wire.Stop()
	_ = s.tunnel.Close()
}

// connect dials the server and completes the handshake and key confirmation.
// The tunnel keeps its handshake deadline; callers clear it once their own
// open request succeeds.
func (c *Client) connect(ctx context.Context) (*session, error) {
	tunnel, err := c.dialServer(ctx)
	if err != nil {
		return nil, err
	}
	if tcp, ok := tunnel.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(60 * time.Second)
		_ = tcp.SetNoDelay(true)
	}
	_ = tunnel.SetDeadline(time.Now().Add(c.handshakeTimeout))
	wire, err := c.provider.NewConn(tunnel, "c2s")
	if err != nil {
		_ = tunnel.Close()
		return nil, err
	}
	s := &session{tunnel: tunnel, wire: wire}
	if err := c.handshake(s); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (c *Client) handshake(s *session) error {
	clientHello, clientPrivate, err := protocol.NewClientHello(c.root, c.provider.Identity(), c.cfg.ClientID, time.Now())
	if err != nil {
		return err
	}
	if err = s.envelope(clientHello); err != nil {
		return err
	}
	serverHello, err := s.wire.ReadEnvelope()
	s.wire.SetPhase("handshake")
	if err == nil && serverHello.Kind == "error" {
		return remoteError(serverHello)
	}
	if err != nil || protocol.ValidateServerHello(serverHello, clientHello, c.root, c.provider.Identity(), time.Now()) != nil {
		return errors.New("server authentication failed")
	}
	s.crypto, err = protocol.NewSession(c.root, c.provider.Identity(), clientHello, serverHello, clientPrivate, "c2s")
	if err != nil {
		return err
	}
	if err = s.frame(protocol.Frame{Type: protocol.FrameKeyConfirm, Payload: s.crypto.Confirmation("client")}); err != nil {
		return err
	}
	frame, err := s.readFrame()
	if err != nil {
		return err
	}
	if frame.Type != protocol.FrameKeyConfirm || !s.crypto.VerifyConfirmation("server", frame.Payload) {
		return errors.New("server key confirmation failed")
	}
	s.wire.SetPhase("established")
	s.wire.StartScheduler()
	return nil
}

func (c *Client) dialServer(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, c.connectTimeout)
	defer cancel()
	host, port, err := net.SplitHostPort(c.cfg.Server)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(host); err == nil || c.resolve == nil {
		return c.dial(ctx, "tcp", c.cfg.Server)
	}
	addrs, err := c.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, addr := range addrs {
		conn, err := c.dial(ctx, "tcp", net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no address for %s", host)
	}
	return nil, errors.Join(errs...)
}

func remoteError(env protocol.Envelope) error {
	if env.Code == "" || env.Message == "" {
		return errors.New("server rejected request")
	}
	return fmt.Errorf("server rejected request: %s: %s", env.Code, env.Message)
}
