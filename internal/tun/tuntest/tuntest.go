// Package tuntest connects an engine to an in-memory "app" network stack,
// so tests can open TCP and UDP flows through the TUN path without a real
// VPN interface or root privileges.
package tuntest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	"supaproto/internal/tun"
)

// AppAddr is the address the simulated app sends from.
var AppAddr = netip.MustParseAddr("10.7.0.2")

const mtu = 1500

// device is the engine's end of the packet pipe.
type device struct {
	in        chan []byte // packets from the app
	app       *channel.Endpoint
	closeOnce sync.Once
	closed    chan struct{}
}

func (d *device) ReadPacket(buf []byte) (int, error) {
	select {
	case p := <-d.in:
		return copy(buf, p), nil
	case <-d.closed:
		return 0, net.ErrClosed
	}
}

func (d *device) WritePacket(p []byte) error {
	select {
	case <-d.closed:
		return net.ErrClosed
	default:
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), p...))})
	d.app.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
	return nil
}

func (d *device) Close() error {
	d.closeOnce.Do(func() { close(d.closed) })
	return nil
}

// App is the simulated device-side stack.
type App struct {
	s      *stack.Stack
	cancel context.CancelFunc
}

// New returns the engine's Device and the app stack that talks to it.
func New() (tun.Device, *App, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(1024, mtu, "")
	if err := s.CreateNIC(1, ep); err != nil {
		return nil, nil, fmt.Errorf("create NIC: %s", err)
	}
	addr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4(AppAddr.As4()).WithPrefix(),
	}
	if err := s.AddProtocolAddress(1, addr, stack.AddressProperties{}); err != nil {
		return nil, nil, fmt.Errorf("add address: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	dev := &device{in: make(chan []byte, 1024), app: ep, closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			pkt := ep.ReadContext(ctx)
			if pkt == nil {
				return
			}
			view := pkt.ToView()
			data := append([]byte(nil), view.AsSlice()...)
			view.Release()
			pkt.DecRef()
			select {
			case dev.in <- data:
			case <-dev.closed:
				return
			}
		}
	}()
	return dev, &App{s: s, cancel: cancel}, nil
}

func fullAddr(a netip.AddrPort) tcpip.FullAddress {
	return tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(a.Addr().As4()), Port: a.Port()}
}

// DialTCP connects from the app to dst through the TUN path.
func (a *App) DialTCP(ctx context.Context, dst netip.AddrPort) (*gonet.TCPConn, error) {
	if !dst.Addr().Is4() {
		return nil, errors.New("tuntest only supports IPv4")
	}
	return gonet.DialContextTCP(ctx, a.s, fullAddr(dst), ipv4.ProtocolNumber)
}

// DialUDP opens a connected UDP socket from the app to dst.
func (a *App) DialUDP(dst netip.AddrPort) (*gonet.UDPConn, error) {
	if !dst.Addr().Is4() {
		return nil, errors.New("tuntest only supports IPv4")
	}
	remote := fullAddr(dst)
	return gonet.DialUDP(a.s, nil, &remote, ipv4.ProtocolNumber)
}

func (a *App) Close() {
	a.cancel()
	a.s.Close()
}
