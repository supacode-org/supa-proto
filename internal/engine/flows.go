package engine

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"supaproto/internal/client"
	"supaproto/internal/protocol"
	"supaproto/internal/router"
	"supaproto/internal/stats"
	"supaproto/internal/tun"
)

const (
	dnsPort        = 53
	dnsIdleTimeout = 30 * time.Second
	udpBufferSize  = 65535
)

func (e *Engine) HandleTCP(req *tun.TCPRequest) {
	if e.tunnelAddress(req.Dst.Addr()) {
		// Nothing listens on the interface itself. Refusing at once lets
		// callers such as Android's Private DNS (DoT on port 853) fall back
		// to plain DNS instead of waiting for a dial timeout.
		e.logf(LogDebug, "tcp %s: tunnel address, refused", req.Dst)
		req.Reject()
		return
	}
	t, ok := e.target(req.Dst.Addr())
	if !ok {
		// A fake IP whose mapping was evicted; the app must look it up again.
		e.logf(LogWarn, "tcp %s: unknown fake IP", req.Dst)
		req.Reject()
		return
	}
	ctx := e.ctx
	port := req.Dst.Port()
	d, flow := e.route(ctx, stats.NetworkTCP, t, port)
	if d.ResolveErr != nil {
		e.logf(LogWarn, "tcp %s: lookup for routing failed: %v", describe(t, port), d.ResolveErr)
	}
	e.logf(LogDebug, "tcp %s %s via %s", describe(t, port), d.Action, d.Rule)

	switch d.Action {
	case router.Reject:
		flow.Fail(nil)
		req.Reject()
	case router.Direct:
		addrs, err := e.directAddrs(ctx, t, d)
		var up net.Conn
		if err == nil {
			up, err = e.dialDirect(ctx, "tcp", addrs, port)
		}
		if err != nil {
			e.logf(LogWarn, "tcp %s DIRECT failed: %v", describe(t, port), err)
			flow.Fail(err)
			req.Reject()
			return
		}
		defer up.Close()
		local, err := req.Accept()
		if err != nil {
			flow.Fail(err)
			return
		}
		defer local.Close()
		defer e.conns.add(closeConn(local), closeConn(up))()
		flow.Start()
		defer flow.Close()
		pipe(flow.Wrap(local), up)
	case router.Proxy:
		stream, err := e.client.DialTCP(ctx, proxyHost(t), port)
		e.proxyResult(err)
		if err != nil {
			// Never fall back to a direct connection.
			e.logf(LogWarn, "tcp %s PROXY failed: %v", describe(t, port), err)
			flow.Fail(err)
			req.Reject()
			return
		}
		defer stream.Close()
		local, err := req.Accept()
		if err != nil {
			flow.Fail(err)
			return
		}
		defer local.Close()
		defer e.conns.add(closeConn(local), func() { stream.Close() })()
		flow.Start()
		defer flow.Close()
		if _, _, err = stream.Relay(flow.Wrap(local), nil); err != nil {
			e.logf(LogDebug, "tcp %s relay ended: %v", describe(t, port), err)
		}
	}
}

func (e *Engine) HandleUDP(src, dst netip.AddrPort, conn net.Conn) {
	defer conn.Close()
	if dst.Port() == dnsPort {
		e.serveDNS(conn)
		return
	}
	if e.tunnelAddress(dst.Addr()) {
		e.logf(LogDebug, "udp %s: tunnel address, dropped", dst)
		return
	}
	t, ok := e.target(dst.Addr())
	if !ok {
		e.logf(LogWarn, "udp %s: unknown fake IP", dst)
		return
	}
	ctx := e.ctx
	port := dst.Port()
	d, flow := e.route(ctx, stats.NetworkUDP, t, port)
	e.logf(LogDebug, "udp %s %s via %s", describe(t, port), d.Action, d.Rule)
	switch d.Action {
	case router.Reject:
		flow.Fail(nil)
	case router.Direct:
		addrs, err := e.directAddrs(ctx, t, d)
		var up net.Conn
		if err == nil {
			up, err = e.dialDirect(ctx, "udp", addrs, port)
		}
		if err != nil {
			e.logf(LogWarn, "udp %s DIRECT failed: %v", describe(t, port), err)
			flow.Fail(err)
			return
		}
		defer up.Close()
		defer e.conns.add(closeConn(conn), closeConn(up))()
		flow.Start()
		defer flow.Close()
		e.relayUDPDirect(flow.Wrap(conn), up)
	case router.Proxy:
		assoc, err := e.client.OpenUDP(ctx)
		e.proxyResult(err)
		if err != nil {
			e.logf(LogWarn, "udp %s PROXY failed: %v", describe(t, port), err)
			flow.Fail(err)
			return
		}
		defer assoc.Close()
		defer e.conns.add(closeConn(conn), func() { assoc.Close() })()
		flow.Start()
		defer flow.Close()
		e.relayUDPProxy(flow.Wrap(conn), assoc, proxyHost(t), port)
	}
}

// serveDNS answers queries from one app socket until it goes idle.
func (e *Engine) serveDNS(conn net.Conn) {
	buf := make([]byte, udpBufferSize)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(dnsIdleTimeout))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		resp := e.pool.HandleQuery(e.ctx, buf[:n], e.doh.Exchange)
		if resp == nil {
			continue
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

func (e *Engine) relayUDPDirect(local, up net.Conn) {
	idle := e.client.UDPIdleTimeout()
	var last atomic.Int64
	touch := func() { last.Store(time.Now().UnixNano()) }
	touch()
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); _ = local.Close(); _ = up.Close() }) }
	copyDatagrams := func(dst, src net.Conn) {
		defer stop()
		buf := make([]byte, udpBufferSize)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			touch()
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}
	go copyDatagrams(up, local)
	go copyDatagrams(local, up)
	watchIdle(done, &last, idle, stop)
}

func (e *Engine) relayUDPProxy(local net.Conn, assoc *client.UDPAssociation, host string, port uint16) {
	idle := e.client.UDPIdleTimeout()
	maxDatagram := e.client.Config().UDP.MaxDatagramSize
	var last atomic.Int64
	touch := func() { last.Store(time.Now().UnixNano()) }
	touch()
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); _ = local.Close(); assoc.Close() }) }

	go func() {
		defer stop()
		buf := make([]byte, udpBufferSize)
		for {
			n, err := local.Read(buf)
			if err != nil {
				_ = assoc.SendClose(2, "local closed")
				return
			}
			if n > maxDatagram {
				continue
			}
			payload, err := protocol.UDPDatagramPayload(host, port, buf[:n])
			if err != nil {
				continue
			}
			touch()
			if err := assoc.Send(payload); err != nil {
				return
			}
		}
	}()
	go func() {
		defer stop()
		for {
			// Replies all belong to this flow whatever address they carry.
			_, _, data, err := assoc.Receive()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					e.logf(LogDebug, "udp %s association ended: %v", host, err)
				}
				return
			}
			touch()
			if _, err := local.Write(data); err != nil {
				return
			}
		}
	}()
	watchIdle(done, &last, idle, func() {
		_ = assoc.SendClose(1, "idle timeout")
		stop()
	})
}

// watchIdle calls onIdle once nothing has moved for idle, or returns when
// done closes.
func watchIdle(done <-chan struct{}, last *atomic.Int64, idle time.Duration, onIdle func()) {
	ticker := time.NewTicker(min(idle, time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, last.Load())) >= idle {
				onIdle()
				return
			}
		}
	}
}

func closeConn(c net.Conn) func() { return func() { _ = c.Close() } }

// pipe copies both directions. A clean EOF half-closes the other side; an
// error tears down both so the opposite copy cannot hang.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			_ = a.Close()
			_ = b.Close()
			return
		}
		client.CloseWrite(dst)
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
}

var _ tun.Handler = (*Engine)(nil)
