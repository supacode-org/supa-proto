package client

import (
	"context"
	"errors"
	"io"
	"time"

	"supaproto/internal/protocol"
)

// UDPAssociation carries datagrams for many destinations over one session.
type UDPAssociation struct {
	s           *session
	maxDatagram int
}

// OpenUDP opens a session and a UDP association on it. A refusal by the
// server is returned as *OpenError.
func (c *Client) OpenUDP(ctx context.Context) (*UDPAssociation, error) {
	s, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.frame(protocol.Frame{Type: protocol.FrameUDPOpen, Payload: protocol.UDPOpenPayload()}); err != nil {
		s.close()
		return nil, err
	}
	frame, err := s.readFrame()
	if err != nil || frame.Type != protocol.FrameUDPOpenResult {
		s.close()
		if err == nil {
			err = errors.New("invalid UDP_OPEN_RESULT")
		}
		return nil, err
	}
	code, message, err := protocol.ParseResult(frame.Payload)
	if err != nil || code != 0 {
		s.close()
		return nil, &OpenError{Code: code, Message: message}
	}
	_ = s.tunnel.SetDeadline(time.Time{})
	return &UDPAssociation{s: s, maxDatagram: c.cfg.UDP.MaxDatagramSize}, nil
}

func (a *UDPAssociation) SessionID() string { return a.s.ID() }

// Send forwards one datagram encoded with protocol.UDPDatagramPayload.
// Callers encode first so a bad destination drops only that datagram.
func (a *UDPAssociation) Send(payload []byte) error {
	return a.s.frame(protocol.Frame{Type: protocol.FrameUDPDatagram, Payload: payload})
}

// Receive returns the next datagram and the address it came from. It answers
// pings itself and returns io.EOF when the server closes the association.
func (a *UDPAssociation) Receive() (string, uint16, []byte, error) {
	for {
		frame, err := a.s.readFrame()
		if err != nil {
			return "", 0, nil, err
		}
		switch frame.Type {
		case protocol.FrameUDPDatagram:
			host, port, data, err := protocol.ParseUDPDatagram(frame.Payload)
			if err != nil || len(data) > a.maxDatagram {
				return "", 0, nil, errors.New("invalid server UDP datagram")
			}
			return host, port, data, nil
		case protocol.FramePing:
			if err := a.s.frame(protocol.Frame{Type: protocol.FramePong, Payload: frame.Payload}); err != nil {
				return "", 0, nil, err
			}
		case protocol.FrameUDPClose:
			return "", 0, nil, io.EOF
		case protocol.FrameReset:
			return "", 0, nil, errors.New("server reset")
		default:
			return "", 0, nil, errors.New("invalid server UDP frame")
		}
	}
}

// SendClose tells the server the association is ending, with a result code
// (1 idle timeout, 2 local side closed).
func (a *UDPAssociation) SendClose(code byte, message string) error {
	return a.s.frame(protocol.Frame{Type: protocol.FrameUDPClose, Payload: protocol.Result(code, message)})
}

// Close tears down the session without notifying the server.
func (a *UDPAssociation) Close() { a.s.close() }
