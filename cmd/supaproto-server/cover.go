package main

import (
	"context"
	cryptorand "crypto/rand"
	"net"
	"sync"
	"time"

	"supaproto/internal/obfs"
	"supaproto/internal/protocol"
)

// serveCover answers a cover-traffic session: it carries no real target and
// either streams steady decoy data to the client or sinks the client's,
// depending on the provider's cover direction. Per-user and global limits
// protect the server's bandwidth. See the cover-traffic spec.
func (a *app) serveCover(tunnel net.Conn, wire *obfs.Conn, writer *frameWriter, session *protocol.Session,
	clientID, source string, started time.Time, payload []byte) {
	if !a.cfg.Cover.Enabled {
		_ = writer.frame(protocol.Frame{Type: protocol.FrameCoverOpenResult, Payload: protocol.Result(1, "cover disabled")})
		a.log.Printf("cover rejected source=%s user=%s session=%s reason=cover_disabled", source, clientID, session.ID())
		return
	}
	profile := a.provider.Cover()
	if profile == nil {
		_ = writer.frame(protocol.Frame{Type: protocol.FrameCoverOpenResult, Payload: protocol.Result(1, "cover unsupported")})
		a.log.Printf("cover rejected source=%s user=%s session=%s reason=cover_unsupported", source, clientID, session.ID())
		return
	}
	rate, err := protocol.ParseCoverOpen(payload)
	if err != nil {
		_ = writer.presetError(errorInvalidRequest, "invalid request")
		return
	}
	effective := int(rate)
	if effective > a.cfg.Cover.MaxRateKbpsPerUser {
		effective = a.cfg.Cover.MaxRateKbpsPerUser
	}
	if effective < 1 {
		effective = 1
	}
	release, ok := a.claimCover(clientID, effective)
	if !ok {
		_ = writer.frame(protocol.Frame{Type: protocol.FrameCoverOpenResult, Payload: protocol.Result(2, "cover limit exceeded")})
		a.log.Printf("cover rejected source=%s user=%s session=%s reason=cover_limit", source, clientID, session.ID())
		return
	}
	defer release()
	if err = writer.frame(protocol.Frame{Type: protocol.FrameCoverOpenResult, Payload: protocol.Result(0, "")}); err != nil {
		return
	}
	_ = tunnel.SetDeadline(time.Time{})
	a.log.Printf("cover established source=%s user=%s session=%s direction=%s rate=%dkbps", source, clientID, session.ID(), profile.Direction, effective)
	a.runCover(tunnel, wire, writer, session, profile, effective)
	a.log.Printf("cover closed source=%s user=%s session=%s duration=%s", source, clientID, session.ID(), time.Since(started).Round(time.Millisecond))
}

// runCover drives one cover session until the client ends it, the connection
// goes idle, or a write fails.
func (a *app) runCover(tunnel net.Conn, wire *obfs.Conn, writer *frameWriter, session *protocol.Session,
	profile *obfs.CoverProfile, rate int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		for ctx.Err() == nil {
			_ = tunnel.SetReadDeadline(time.Now().Add(a.idleTimeout))
			frame, err := readFrame(wire, session)
			if err != nil {
				return
			}
			switch frame.Type {
			case protocol.FrameData:
				// Discard: cover data carries nothing.
			case protocol.FramePing:
				if err = writer.frame(protocol.Frame{Type: protocol.FramePong, Payload: frame.Payload}); err != nil {
					return
				}
			case protocol.FrameReset:
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		if profile.Direction == "s2c" {
			a.coverGenerate(ctx, writer, profile, rate)
			return
		}
		<-ctx.Done() // c2s: the server only sinks; the client generates.
	}()
	<-ctx.Done()
	_ = tunnel.Close()
	wg.Wait()
}

// coverGenerate paces generated cover data to the target rate (s2c).
func (a *app) coverGenerate(ctx context.Context, writer *frameWriter, profile *obfs.CoverProfile, rate int) {
	buf := make([]byte, profile.ChunkBytesMax)
	_, _ = cryptorand.Read(buf)
	shaper := obfs.NewShaper(*profile, rate)
	for {
		size, delay := shaper.Next()
		if !sleepCtx(ctx, delay) {
			return
		}
		if size > len(buf) {
			size = len(buf)
		}
		if err := writer.frame(protocol.Frame{Type: protocol.FrameData, Payload: buf[:size]}); err != nil {
			return
		}
	}
}

// claimCover admits one cover session for clientID at the given rate, enforcing
// the per-user session count, per-user rate and global rate limits. It returns
// a release function and whether admission succeeded.
func (a *app) claimCover(clientID string, rate int) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.coverActive[clientID] >= a.cfg.Cover.MaxSessionsPerUser {
		return nil, false
	}
	if a.coverRate[clientID]+rate > a.cfg.Cover.MaxRateKbpsPerUser {
		return nil, false
	}
	if limit := a.cfg.Cover.MaxTotalRateMbps; limit > 0 && a.coverTotal+rate > limit*1000 {
		return nil, false
	}
	a.coverActive[clientID]++
	a.coverRate[clientID] += rate
	a.coverTotal += rate
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.coverActive[clientID]--
			a.coverRate[clientID] -= rate
			a.coverTotal -= rate
			a.mu.Unlock()
		})
	}, true
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
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
