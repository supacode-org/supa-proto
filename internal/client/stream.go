package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"supaproto/internal/flow"
	"supaproto/internal/protocol"
)

// ErrInvalidTarget reports a target address the protocol cannot encode.
var ErrInvalidTarget = errors.New("invalid target address")

// Stream is an opened TCP target behind one session.
type Stream struct {
	s           *session
	idleTimeout time.Duration
	mode        flow.Mode
}

// DialTCP opens a session and asks the server to connect to host:port. A
// refusal by the server is returned as *OpenError and an address the
// protocol cannot carry as ErrInvalidTarget.
func (c *Client) DialTCP(ctx context.Context, host string, port uint16) (*Stream, error) {
	open, err := protocol.OpenPayload(host, port)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	s, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.frame(protocol.Frame{Type: protocol.FrameOpen, Payload: open}); err != nil {
		s.close()
		return nil, err
	}
	frame, err := s.readFrame()
	if err != nil {
		s.close()
		return nil, err
	}
	if frame.Type != protocol.FrameOpenResult {
		s.close()
		return nil, errors.New("invalid OPEN_RESULT")
	}
	code, message, err := protocol.ParseResult(frame.Payload)
	if err != nil || code != 0 {
		s.close()
		return nil, &OpenError{Code: code, Message: message}
	}
	_ = s.tunnel.SetDeadline(time.Time{})
	return &Stream{s: s, idleTimeout: c.idleTimeout, mode: flow.Mode(c.cfg.Performance.Mode)}, nil
}

func (st *Stream) SessionID() string { return st.s.ID() }

func (st *Stream) Close() { st.s.close() }

// Relay copies data between local and the target until both directions
// finish. pending is data already read from local that must be sent first.
// It returns the bytes sent up and received down.
func (st *Stream) Relay(local net.Conn, pending []byte) (uint64, uint64, error) {
	s := st.s
	var bytesUp, bytesDown atomic.Uint64
	if len(pending) > 0 {
		if err := s.frame(protocol.Frame{Type: protocol.FrameData, Payload: pending}); err != nil {
			return 0, 0, err
		}
		bytesUp.Add(uint64(len(pending)))
	}
	downDone := make(chan error, 1)
	go func() {
		serverClosed := false
		finish := func(err error) {
			if err != nil {
				_ = local.Close()
			}
			downDone <- err
		}
		for {
			_ = s.tunnel.SetReadDeadline(time.Now().Add(st.idleTimeout))
			frame, err := s.readFrame()
			if err != nil {
				if serverClosed && flow.IsNormalClose(err) {
					err = nil
				}
				finish(err)
				return
			}
			switch frame.Type {
			case protocol.FrameData:
				if serverClosed {
					finish(errors.New("server sent data after CLOSE_WRITE"))
					return
				}
				if err = writeConn(local, frame.Payload, writeTimeout); err != nil {
					finish(err)
					return
				}
				bytesDown.Add(uint64(len(frame.Payload)))
			case protocol.FrameCloseWrite:
				serverClosed = true
				CloseWrite(local)
			case protocol.FramePing:
				if err = s.frame(protocol.Frame{Type: protocol.FramePong, Payload: frame.Payload}); err != nil {
					finish(err)
					return
				}
			case protocol.FrameReset:
				finish(errors.New("server reset"))
				return
			default:
				finish(errors.New("invalid server frame"))
				return
			}
		}
	}()

	adaptive := flow.NewAdaptiveReader(st.mode)
	for {
		payload, readErr := adaptive.ReadFrame(local)
		if len(payload) != 0 {
			started := time.Now()
			sendErr := s.frame(protocol.Frame{Type: protocol.FrameData, Payload: payload})
			adaptive.Observe(len(payload), time.Since(started), sendErr)
			bytesUp.Add(uint64(len(payload)))
			flow.ReleaseFrame(payload)
			if sendErr != nil {
				return bytesUp.Load(), bytesDown.Load(), sendErr
			}
		}
		if readErr != nil {
			if err := s.frame(protocol.Frame{Type: protocol.FrameCloseWrite}); err != nil {
				return bytesUp.Load(), bytesDown.Load(), err
			}
			CloseWrite(s.tunnel)
			if !flow.IsNormalClose(readErr) {
				return bytesUp.Load(), bytesDown.Load(), readErr
			}
			break
		}
	}
	err := <-downDone
	return bytesUp.Load(), bytesDown.Load(), err
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

// CloseWrite half-closes conn when it supports it, otherwise closes it.
func CloseWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	} else {
		_ = conn.Close()
	}
}
