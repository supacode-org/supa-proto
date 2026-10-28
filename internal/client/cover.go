package client

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"json2proxy/internal/obfs"
	"json2proxy/internal/protocol"
)

// Cover log levels, matching the engine's.
const (
	coverLogDebug = 0
	coverLogInfo  = 1
	coverLogWarn  = 2
)

// Hard-coded cover-connection rules. These are not configurable: the user only
// sets the aggregate target rate. Per-connection rate, count, lifetime and
// spacing are derived here with randomness so the pattern resists analysis.
const (
	coverMaxSessions     = 32
	coverMinPerConnKbps  = 128
	coverMaxPerConnKbps  = 256
	coverMeanPerConnKbps = 192
	coverStaggerMax      = 8 * time.Second
	coverReprobeMin      = 25 * time.Minute
	coverReprobeMax      = 35 * time.Minute
	coverLongFraction    = 0.65 // share of connections that stay long-lived
)

// CoverStats receives cover-session lifecycle and byte events so a host can
// account for cover traffic separately from real flows. Methods may be called
// concurrently; a nil CoverStats disables reporting.
type CoverStats interface {
	CoverSessionOpened()
	CoverSessionClosed()
	CoverBytes(up, down int)
}

// CoverOptions configures a cover-traffic manager.
type CoverOptions struct {
	// RateKbps is the aggregate target wire throughput across all cover
	// connections. Zero or negative disables cover.
	RateKbps int
	Log      func(level int, tag, message string)
	Stats    CoverStats
}

// Cover runs cross-connection cover traffic for a client until Close. It opens
// up to coverMaxSessions mock sessions that carry no real target, generating
// (or sinking) steady decoy data in the provider's cover direction.
type Cover struct {
	client  *Client
	opts    CoverOptions
	profile obfs.CoverProfile
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu        sync.Mutex
	supported bool
	nextProbe time.Time
	sessions  map[*session]struct{}
}

// StartCover begins cover traffic. It returns a no-op manager when the active
// provider does not support cover or RateKbps is not positive.
func (c *Client) StartCover(ctx context.Context, opts CoverOptions) *Cover {
	profile := c.provider.Cover()
	if profile == nil || opts.RateKbps <= 0 {
		return &Cover{}
	}
	cctx, cancel := context.WithCancel(ctx)
	cv := &Cover{
		client: c, opts: opts, profile: *profile,
		ctx: cctx, cancel: cancel, supported: true,
		sessions: make(map[*session]struct{}),
	}
	rates := planRates(opts.RateKbps)
	cv.logf(coverLogInfo, "cover starting rate=%dkbps sessions=%d direction=%s provider=%s",
		opts.RateKbps, len(rates), profile.Direction, c.provider.ID())
	for i, r := range rates {
		cv.wg.Add(1)
		go cv.slot(i, r)
	}
	return cv
}

// Close stops all cover traffic and waits for its goroutines to finish.
func (cv *Cover) Close() {
	if cv.cancel == nil {
		return
	}
	cv.cancel()
	cv.closeSessions()
	cv.wg.Wait()
}

// NetworkChanged drops the current cover sessions; the manager reconnects them
// over the current network. It is a no-op on a stopped or no-op manager.
func (cv *Cover) NetworkChanged() {
	if cv.cancel == nil || cv.ctx.Err() != nil {
		return
	}
	cv.closeSessions()
}

// planRates splits the aggregate target into per-connection rates, each within
// the hard-coded 128-256 kbps band, with a count derived from the mean and
// capped at coverMaxSessions.
func planRates(total int) []int {
	n := int(math.Round(float64(total) / coverMeanPerConnKbps))
	if n < 1 {
		n = 1
	}
	if n > coverMaxSessions {
		n = coverMaxSessions
	}
	rates := make([]int, n)
	remaining := total
	for i := range rates {
		slots := n - i
		base := remaining / slots
		// Jitter each connection ±20% so no two look identical, then clamp to
		// the band. The last connection absorbs the rounding remainder.
		r := base
		if slots > 1 {
			r = base + int(float64(base)*0.2*(rand.Float64()*2-1))
		}
		if r < coverMinPerConnKbps {
			r = coverMinPerConnKbps
		}
		if r > coverMaxPerConnKbps {
			r = coverMaxPerConnKbps
		}
		rates[i] = r
		remaining -= r
		if remaining < 0 {
			remaining = 0
		}
	}
	return rates
}

func (cv *Cover) slot(idx, perConn int) {
	defer cv.wg.Done()
	longLived := rand.Float64() < coverLongFraction
	if !cv.sleep(randDuration(0, coverStaggerMax)) {
		return
	}
	for {
		if cv.ctx.Err() != nil {
			return
		}
		if !cv.claimDial() {
			if !cv.sleep(randDuration(15*time.Second, 45*time.Second)) {
				return
			}
			continue
		}
		s, declined, err := cv.client.dialCover(cv.ctx, uint16(perConn))
		if err != nil {
			if declined {
				cv.markUnsupported()
				cv.logf(coverLogWarn, "cover declined by server, backing off: %v", err)
			} else {
				cv.logf(coverLogDebug, "cover dial failed: %v", err)
				if !cv.sleep(randDuration(3*time.Second, 10*time.Second)) {
					return
				}
			}
			continue
		}
		cv.markSupported()
		cv.register(s)
		cv.runSession(s, perConn, cv.pickLifetime(longLived))
		cv.unregister(s)
		if !cv.sleep(cv.pickGap(longLived)) {
			return
		}
	}
}

func (cv *Cover) pickLifetime(longLived bool) time.Duration {
	if longLived {
		return randDuration(30*time.Minute, 120*time.Minute)
	}
	return randDuration(20*time.Second, 180*time.Second)
}

func (cv *Cover) pickGap(longLived bool) time.Duration {
	if longLived {
		return randDuration(1*time.Second, 8*time.Second)
	}
	return randDuration(10*time.Second, 90*time.Second)
}

// runSession drives one established cover session until its lifetime expires,
// the manager stops, or the peer ends it. It closes the session on return.
func (cv *Cover) runSession(s *session, perConn int, lifetime time.Duration) {
	sctx, cancel := context.WithTimeout(cv.ctx, lifetime)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); defer cancel(); cv.readLoop(s, sctx) }()
	go func() { defer wg.Done(); defer cancel(); cv.writeLoop(s, sctx, perConn) }()
	<-sctx.Done()
	s.close()
	wg.Wait()
}

// readLoop drains inbound frames, answering pings and counting received cover
// bytes, until the session context ends or the peer closes.
func (cv *Cover) readLoop(s *session, ctx context.Context) {
	for ctx.Err() == nil {
		_ = s.tunnel.SetReadDeadline(time.Now().Add(cv.client.idleTimeout))
		frame, err := s.readFrame()
		if err != nil {
			return
		}
		switch frame.Type {
		case protocol.FrameData:
			cv.addBytes(0, len(frame.Payload))
		case protocol.FramePing:
			if err = s.frame(protocol.Frame{Type: protocol.FramePong, Payload: frame.Payload}); err != nil {
				return
			}
		case protocol.FrameReset:
			return
		}
	}
}

func (cv *Cover) writeLoop(s *session, ctx context.Context, perConn int) {
	buf := make([]byte, cv.profile.ChunkBytesMax)
	_, _ = cryptorand.Read(buf)
	if cv.profile.Direction == "c2s" {
		cv.generate(s, ctx, perConn, buf)
		return
	}
	cv.requestLoop(s, ctx, buf)
}

// generate paces outbound cover data to the per-connection rate (c2s).
func (cv *Cover) generate(s *session, ctx context.Context, perConn int, buf []byte) {
	shaper := obfs.NewShaper(cv.profile, perConn)
	for {
		size, delay := shaper.Next()
		if !cv.sleepCtx(ctx, delay) {
			return
		}
		if size > len(buf) {
			size = len(buf)
		}
		if err := s.frame(protocol.Frame{Type: protocol.FrameData, Payload: buf[:size]}); err != nil {
			return
		}
		cv.addBytes(size, 0)
	}
}

// requestLoop sends occasional small upstream frames while the server streams
// down (s2c), mimicking a media player's segment requests.
func (cv *Cover) requestLoop(s *session, ctx context.Context, buf []byte) {
	for {
		if !cv.sleepCtx(ctx, randDuration(2*time.Second, 6*time.Second)) {
			return
		}
		size := 16 + rand.IntN(104)
		if err := s.frame(protocol.Frame{Type: protocol.FrameData, Payload: buf[:size]}); err != nil {
			return
		}
		cv.addBytes(size, 0)
	}
}

// dialCover establishes a session and opens it as a cover session. declined is
// true when the server completed the handshake but refused the cover frame
// (an old server rejecting the unknown frame, or a new server declining),
// which tells the manager to stop and re-probe later.
func (c *Client) dialCover(ctx context.Context, rateKbps uint16) (s *session, declined bool, err error) {
	s, err = c.connect(ctx)
	if err != nil {
		return nil, false, err
	}
	if err = s.frame(protocol.Frame{Type: protocol.FrameCoverOpen, Payload: protocol.CoverOpenPayload(rateKbps)}); err != nil {
		s.close()
		return nil, false, err
	}
	frame, err := s.readFrame()
	if err != nil {
		s.close()
		return nil, true, err
	}
	if frame.Type != protocol.FrameCoverOpenResult {
		s.close()
		return nil, true, errors.New("invalid COVER_OPEN_RESULT")
	}
	code, message, perr := protocol.ParseResult(frame.Payload)
	if perr != nil || code != 0 {
		s.close()
		return nil, true, &OpenError{Code: code, Message: message}
	}
	_ = s.tunnel.SetDeadline(time.Time{})
	return s, false, nil
}

func (cv *Cover) claimDial() bool {
	cv.mu.Lock()
	defer cv.mu.Unlock()
	if cv.supported {
		return true
	}
	if time.Now().Before(cv.nextProbe) {
		return false
	}
	cv.nextProbe = time.Now().Add(randDuration(coverReprobeMin, coverReprobeMax))
	return true
}

func (cv *Cover) markUnsupported() {
	cv.mu.Lock()
	if cv.supported {
		cv.supported = false
		cv.nextProbe = time.Now().Add(randDuration(coverReprobeMin, coverReprobeMax))
	}
	cv.mu.Unlock()
}

func (cv *Cover) markSupported() {
	cv.mu.Lock()
	cv.supported = true
	cv.mu.Unlock()
}

func (cv *Cover) register(s *session) {
	cv.mu.Lock()
	cv.sessions[s] = struct{}{}
	cv.mu.Unlock()
	if cv.opts.Stats != nil {
		cv.opts.Stats.CoverSessionOpened()
	}
}

func (cv *Cover) unregister(s *session) {
	cv.mu.Lock()
	_, ok := cv.sessions[s]
	delete(cv.sessions, s)
	cv.mu.Unlock()
	if ok && cv.opts.Stats != nil {
		cv.opts.Stats.CoverSessionClosed()
	}
}

func (cv *Cover) closeSessions() {
	cv.mu.Lock()
	open := make([]*session, 0, len(cv.sessions))
	for s := range cv.sessions {
		open = append(open, s)
	}
	cv.mu.Unlock()
	for _, s := range open {
		s.close()
	}
}

func (cv *Cover) addBytes(up, down int) {
	if cv.opts.Stats != nil {
		cv.opts.Stats.CoverBytes(up, down)
	}
}

func (cv *Cover) sleep(d time.Duration) bool { return cv.sleepCtx(cv.ctx, d) }

func (cv *Cover) sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (cv *Cover) logf(level int, format string, args ...any) {
	if cv.opts.Log != nil {
		cv.opts.Log(level, "Cover", fmt.Sprintf(format, args...))
	}
}

func randDuration(minimum, maximum time.Duration) time.Duration {
	if maximum <= minimum {
		return minimum
	}
	return minimum + time.Duration(rand.Int64N(int64(maximum-minimum)))
}
