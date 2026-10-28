package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"strconv"
	"time"

	"json2proxy/internal/client"
	"json2proxy/internal/config"
	"json2proxy/internal/logging"
	"json2proxy/internal/protocol"
)

type socksServer struct {
	cfg   config.Client
	proxy *client.Client
	log   *logging.Logger
}

func main() {
	configPath := flag.String("config", ".private/configs/client.json", "client config")
	flag.Parse()

	var cfg config.Client
	must(config.Load(*configPath, &cfg))
	proxy, err := client.New(cfg, client.Options{})
	must(err)
	cfg = proxy.Config()
	logger, err := logging.New(cfg.LogFile, cfg.LogSize)
	must(err)
	defer logger.Close()

	c := &socksServer{cfg: cfg, proxy: proxy, log: logger}
	provider := proxy.Provider()

	listener, err := net.Listen("tcp", cfg.SocksListen)
	must(err)
	defer listener.Close()
	logger.Printf("client starting product=JSON2Proxy protocol=v%d socks_listen=%s server=%s log_size=%s performance=%s obfs=%s obfs_version=%s participation=%.3f obfs_hash=%s udp=%t udp_bind=%s cover=%t cover_rate=%dkbps",
		protocol.Version, cfg.SocksListen, cfg.Server, cfg.LogSize, cfg.Performance.Mode,
		provider.ID(), provider.Descriptor.Version, provider.Participation, provider.Hash(), cfg.UDP.IsEnabled(), cfg.UDP.Bind,
		cfg.Cover.Enabled, cfg.Cover.RateKbps)
	if cfg.Cover.Enabled {
		cover := proxy.StartCover(context.Background(), client.CoverOptions{
			RateKbps: cfg.Cover.RateKbps,
			Log:      func(_ int, tag, message string) { logger.Printf("%s %s", tag, message) },
		})
		defer cover.Close()
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.Printf("socks accept failed error=%q", err)
			continue
		}
		go c.serveSOCKS(conn)
	}
}

func (c *socksServer) serveSOCKS(local net.Conn) {
	defer local.Close()
	started := time.Now()
	source := local.RemoteAddr().String()
	reader := bufio.NewReader(local)
	if err := socksHello(reader, local); err != nil {
		c.log.Printf("socks closed source=%s reason=handshake_failed", source)
		return
	}
	request, err := socksRequest(reader)
	if err != nil {
		socksReply(local, 1)
		c.log.Printf("socks closed source=%s reason=request_failed", source)
		return
	}
	if request.Command == 3 {
		if !c.cfg.UDP.IsEnabled() {
			socksReply(local, 7)
			c.log.Printf("socks udp closed source=%s target=- reason=udp_disabled", source)
			return
		}
		c.serveUDP(local, reader, request)
		return
	}
	target := net.JoinHostPort(request.Host, strconv.Itoa(int(request.Port)))
	up, down, sessionID, err := c.tunnel(local, reader, request.Host, request.Port)
	if err != nil {
		c.log.Printf("socks closed source=%s target=%s session=%s bytes_up=%d bytes_down=%d duration=%s reason=tunnel_failed error=%q",
			source, target, sessionID, up, down, time.Since(started).Round(time.Millisecond), err)
		return
	}
	c.log.Printf("socks closed source=%s target=%s session=%s bytes_up=%d bytes_down=%d duration=%s reason=completed",
		source, target, sessionID, up, down, time.Since(started).Round(time.Millisecond))
}

func socksHello(reader *bufio.Reader, writer io.Writer) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 5 || header[1] == 0 {
		return errors.New("invalid SOCKS5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	for _, method := range methods {
		if method == 0 {
			_, err := writer.Write([]byte{5, 0})
			return err
		}
	}
	_, _ = writer.Write([]byte{5, 0xff})
	return errors.New("SOCKS5 client did not offer no-auth")
}

type socksRequestInfo struct {
	Command     byte
	AddressType byte
	Host        string
	Port        uint16
}

func socksRequest(reader *bufio.Reader) (socksRequestInfo, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return socksRequestInfo{}, err
	}
	if header[0] != 5 || (header[1] != 1 && header[1] != 3) || header[2] != 0 {
		return socksRequestInfo{}, errors.New("unsupported SOCKS5 request")
	}
	var host string
	switch header[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(reader, b); err != nil {
			return socksRequestInfo{}, err
		}
		host = net.IP(b).String()
	case 3:
		n, err := reader.ReadByte()
		if err != nil || n == 0 {
			return socksRequestInfo{}, errors.New("invalid SOCKS5 domain")
		}
		b := make([]byte, int(n))
		if _, err = io.ReadFull(reader, b); err != nil {
			return socksRequestInfo{}, err
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(reader, b); err != nil {
			return socksRequestInfo{}, err
		}
		host = net.IP(b).String()
	default:
		return socksRequestInfo{}, errors.New("unsupported SOCKS5 address type")
	}
	p := make([]byte, 2)
	if _, err := io.ReadFull(reader, p); err != nil {
		return socksRequestInfo{}, err
	}
	port := uint16(p[0])<<8 | uint16(p[1])
	if port == 0 && header[1] != 3 {
		return socksRequestInfo{}, errors.New("invalid SOCKS5 port")
	}
	return socksRequestInfo{Command: header[1], AddressType: header[3], Host: host, Port: port}, nil
}

func socksReply(writer io.Writer, code byte) {
	_, _ = writer.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
}

func (c *socksServer) tunnel(local net.Conn, localReader *bufio.Reader, host string, port uint16) (uint64, uint64, string, error) {
	stream, err := c.proxy.DialTCP(context.Background(), host, port)
	if err != nil {
		var refused *client.OpenError
		switch {
		case errors.Is(err, client.ErrInvalidTarget):
			socksReply(local, 8)
		case errors.As(err, &refused):
			socksReply(local, mapResult(refused.Code))
		default:
			socksReply(local, 1)
		}
		return 0, 0, "", err
	}
	defer stream.Close()
	socksReply(local, 0)

	var pending []byte
	if buffered := localReader.Buffered(); buffered > 0 {
		pending = make([]byte, buffered)
		if _, err = io.ReadFull(localReader, pending); err != nil {
			return 0, 0, stream.SessionID(), err
		}
	}
	up, down, err := stream.Relay(local, pending)
	return up, down, stream.SessionID(), err
}

func mapResult(code byte) byte {
	switch code {
	case 1:
		return 2
	case 2:
		return 4
	case 3:
		return 5
	default:
		return 1
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
