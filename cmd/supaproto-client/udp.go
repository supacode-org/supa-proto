package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"supaproto/internal/client"
	"supaproto/internal/protocol"
	"supaproto/internal/socks5"
)

type udpClientStats struct {
	packetsUp, packetsDown atomic.Uint64
	bytesUp, bytesDown     atomic.Uint64
	localDrops             atomic.Uint64
	queueDrops             atomic.Uint64
	oversizeDrops          atomic.Uint64
}

type udpOutboundPacket struct {
	payload []byte
	target  string
	bytes   int
}

type udpEndpoint struct {
	mu   sync.RWMutex
	addr *net.UDPAddr
}

func (e *udpEndpoint) setOrMatch(source *net.UDPAddr, expectedIP net.IP, expectedPort uint16) bool {
	if !source.IP.Equal(expectedIP) || (expectedPort != 0 && source.Port != int(expectedPort)) {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.addr == nil {
		e.addr = &net.UDPAddr{IP: append(net.IP(nil), source.IP...), Port: source.Port, Zone: source.Zone}
	}
	return source.IP.Equal(e.addr.IP) && source.Port == e.addr.Port
}

func (e *udpEndpoint) get() *net.UDPAddr {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.addr == nil {
		return nil
	}
	copyAddr := *e.addr
	copyAddr.IP = append(net.IP(nil), e.addr.IP...)
	return &copyAddr
}

func (c *socksServer) serveUDP(control net.Conn, reader *bufio.Reader, request socksRequestInfo) {
	started := time.Now()
	source := control.RemoteAddr().String()
	if request.AddressType == 3 {
		socksReply(control, 8)
		c.log.Printf("socks udp closed source=%s target=- reason=association_domain_unsupported", source)
		return
	}
	peer, ok := control.RemoteAddr().(*net.TCPAddr)
	if !ok {
		socksReply(control, 1)
		return
	}
	expectedIP := net.ParseIP(request.Host)
	if expectedIP == nil || expectedIP.IsUnspecified() {
		expectedIP = peer.IP
	}
	if !expectedIP.Equal(peer.IP) {
		socksReply(control, 2)
		c.log.Printf("socks udp closed source=%s target=- reason=association_source_denied", source)
		return
	}
	bind, err := net.ResolveUDPAddr("udp", c.cfg.UDP.Bind)
	if err != nil {
		socksReply(control, 1)
		return
	}
	relay, err := net.ListenUDP("udp", bind)
	if err != nil {
		socksReply(control, 1)
		c.log.Printf("socks udp closed source=%s target=- reason=relay_bind_failed error=%q", source, err)
		return
	}
	defer relay.Close()

	assoc, err := c.proxy.OpenUDP(context.Background())
	if err != nil {
		var refused *client.OpenError
		if errors.As(err, &refused) {
			socksReply(control, mapUDPResult(refused.Code))
			c.log.Printf("socks udp closed source=%s target=- reason=open_rejected message=%q", source, refused.Message)
			return
		}
		socksReply(control, 1)
		c.log.Printf("socks udp closed source=%s target=- reason=tunnel_failed error=%q", source, err)
		return
	}
	defer assoc.Close()
	sessionID := assoc.SessionID()
	maxDatagram := c.cfg.UDP.MaxDatagramSize
	bound, _ := relay.LocalAddr().(*net.UDPAddr)
	if err = socksReplyUDP(control, 0, bound); err != nil {
		return
	}

	var endpoint udpEndpoint
	if request.Port != 0 {
		endpoint.addr = &net.UDPAddr{IP: append(net.IP(nil), expectedIP...), Port: int(request.Port)}
	}
	stats := &udpClientStats{}
	var lastTargetMu sync.Mutex
	lastTarget := "-"
	lastActivity := atomic.Int64{}
	lastActivity.Store(time.Now().UnixNano())
	errCh := make(chan error, 4)
	done := make(chan struct{})
	outbound := make(chan udpOutboundPacket, c.cfg.UDP.QueuePackets)
	var closeOnce sync.Once
	var workers sync.WaitGroup
	finish := func(err error) {
		closeOnce.Do(func() {
			close(done)
			_ = relay.Close()
			assoc.Close()
			_ = control.SetReadDeadline(time.Now())
			errCh <- err
		})
	}

	workers.Add(5)
	go func() {
		defer workers.Done()
		_, readErr := reader.ReadByte()
		if readErr == nil {
			readErr = errors.New("unexpected SOCKS5 control data")
		}
		_ = assoc.SendClose(2, "control closed")
		finish(readErr)
	}()

	go func() {
		defer workers.Done()
		buffer := make([]byte, 65535)
		for {
			n, remote, readErr := relay.ReadFromUDP(buffer)
			if readErr != nil {
				finish(readErr)
				return
			}
			if !endpoint.setOrMatch(remote, expectedIP, request.Port) {
				stats.localDrops.Add(1)
				continue
			}
			host, port, data, parseErr := socks5.ParseUDPDatagram(buffer[:n])
			if parseErr != nil {
				stats.localDrops.Add(1)
				continue
			}
			if len(data) > maxDatagram {
				stats.oversizeDrops.Add(1)
				continue
			}
			payload, encodeErr := protocol.UDPDatagramPayload(host, port, data)
			if encodeErr != nil {
				stats.localDrops.Add(1)
				continue
			}
			packet := udpOutboundPacket{
				payload: payload, target: net.JoinHostPort(host, strconv.Itoa(int(port))), bytes: len(data),
			}
			select {
			case outbound <- packet:
				lastActivity.Store(time.Now().UnixNano())
			case <-done:
				return
			default:
				stats.queueDrops.Add(1)
			}
		}
	}()

	go func() {
		defer workers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-done:
				return
			case packet := <-outbound:
				if writeErr := assoc.Send(packet.payload); writeErr != nil {
					finish(writeErr)
					return
				}
				stats.packetsUp.Add(1)
				stats.bytesUp.Add(uint64(packet.bytes))
				lastTargetMu.Lock()
				lastTarget = packet.target
				lastTargetMu.Unlock()
			}
		}
	}()

	go func() {
		defer workers.Done()
		for {
			host, port, data, readErr := assoc.Receive()
			if readErr != nil {
				finish(readErr)
				return
			}
			packet, encodeErr := socks5.EncodeUDPDatagram(host, port, data)
			if encodeErr != nil {
				finish(encodeErr)
				return
			}
			application := endpoint.get()
			if application == nil {
				stats.localDrops.Add(1)
				continue
			}
			if _, writeErr := relay.WriteToUDP(packet, application); writeErr != nil {
				finish(writeErr)
				return
			}
			stats.packetsDown.Add(1)
			stats.bytesDown.Add(uint64(len(data)))
			lastActivity.Store(time.Now().UnixNano())
		}
	}()

	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				last := time.Unix(0, lastActivity.Load())
				if time.Since(last) >= c.proxy.UDPIdleTimeout() {
					_ = assoc.SendClose(1, "idle timeout")
					finish(errors.New("UDP idle timeout"))
					return
				}
			}
		}
	}()

	endErr := <-errCh
	workers.Wait()
	lastTargetMu.Lock()
	target := lastTarget
	lastTargetMu.Unlock()
	reason := "completed"
	if endErr != nil && !errors.Is(endErr, io.EOF) && !errors.Is(endErr, net.ErrClosed) {
		reason = "udp_failed"
	}
	c.log.Printf("socks udp closed source=%s target=%s session=%s packets_up=%d packets_down=%d bytes_up=%d bytes_down=%d local_drops=%d queue_drops=%d oversize_drops=%d duration=%s reason=%s",
		source, target, sessionID, stats.packetsUp.Load(), stats.packetsDown.Load(), stats.bytesUp.Load(),
		stats.bytesDown.Load(), stats.localDrops.Load(), stats.queueDrops.Load(), stats.oversizeDrops.Load(),
		time.Since(started).Round(time.Millisecond), reason)
}

func socksReplyUDP(writer io.Writer, code byte, address *net.UDPAddr) error {
	if address == nil || code != 0 {
		_, err := writer.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
		return err
	}
	ip := address.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}
	response := []byte{5, code, 0}
	if v4 := ip.To4(); v4 != nil {
		response = append(response, 1)
		response = append(response, v4...)
	} else {
		response = append(response, 4)
		response = append(response, ip.To16()...)
	}
	response = append(response, byte(address.Port>>8), byte(address.Port))
	_, err := writer.Write(response)
	return err
}

func mapUDPResult(code byte) byte {
	switch code {
	case 2:
		return 2
	case 4:
		return 7
	default:
		return 1
	}
}
