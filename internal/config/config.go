package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"time"

	"json2proxy/internal/obfs"
)

type User struct {
	ClientID       string `json:"client_id"`
	Secret         string `json:"secret"`
	Status         string `json:"status"`
	MaxConnections int    `json:"max_connections"`
	AllowPrivate   bool   `json:"allow_private"`
}

type Server struct {
	Listen           string      `json:"listen"`
	HandshakeTimeout string      `json:"handshake_timeout"`
	DialTimeout      string      `json:"dial_timeout"`
	IdleTimeout      string      `json:"idle_timeout"`
	LogSize          string      `json:"log_size"`
	LogFile          string      `json:"log_file"`
	Performance      Performance `json:"performance"`
	Obfs             obfs.Config `json:"obfs"`
	Cover            CoverServer `json:"cover"`
	UDP              ServerUDP   `json:"udp"`
	Outbound         Outbound    `json:"outbound,omitempty"`
	Users            []User      `json:"users"`
}

// CoverServer configures whether the server answers cover-traffic sessions and
// the per-user and global limits that protect its bandwidth. Disabled by
// default; see the cover-traffic spec.
type CoverServer struct {
	Enabled            bool `json:"enabled"`
	MaxRateKbpsPerUser int  `json:"max_rate_kbps_per_user"`
	MaxSessionsPerUser int  `json:"max_sessions_per_user"`
	MaxTotalRateMbps   int  `json:"max_total_rate_mbps"`
}

// CoverClient configures whether the client generates cover traffic and the
// target aggregate wire throughput in kbps. Disabled by default.
type CoverClient struct {
	Enabled  bool `json:"enabled"`
	RateKbps int  `json:"rate_kbps"`
}

type ServerUDP struct {
	Enabled                       *bool  `json:"enabled,omitempty"`
	IdleTimeout                   string `json:"idle_timeout"`
	MaxDatagramSize               int    `json:"max_datagram_size"`
	QueuePackets                  int    `json:"queue_packets"`
	MaxAssociationsPerUser        int    `json:"max_associations_per_user"`
	MaxDestinationsPerAssociation int    `json:"max_destinations_per_association"`
	PacketsPerSecondPerUser       int    `json:"packets_per_second_per_user"`
	BytesPerSecondPerUser         int64  `json:"bytes_per_second_per_user"`
	DNSCacheTTL                   string `json:"dns_cache_ttl"`
	DNSNegativeTTL                string `json:"dns_negative_ttl"`
}

type Outbound struct {
	Type     string `json:"type"`
	Address  string `json:"address"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	DNS      string `json:"dns,omitempty"`
}

type Performance struct {
	Mode string `json:"mode"`
}

func (o Outbound) Enabled() bool {
	return o.Type != "" || o.Address != "" || o.Username != "" || o.Password != "" || o.DNS != ""
}

// UsesRemoteDNS reports whether domain names are delegated to the outbound
// SOCKS5 server. In this mode the SOCKS5 server enforces destination policy
// after resolving a name.
func (o Outbound) UsesRemoteDNS() bool {
	return o.Enabled() && o.DNS == "remote"
}

type Client struct {
	SocksListen      string      `json:"socks_listen"`
	Server           string      `json:"server"`
	ConnectTimeout   string      `json:"connect_timeout"`
	HandshakeTimeout string      `json:"handshake_timeout"`
	IdleTimeout      string      `json:"idle_timeout"`
	ClientID         string      `json:"client_id"`
	Secret           string      `json:"secret"`
	LogSize          string      `json:"log_size"`
	LogFile          string      `json:"log_file"`
	Performance      Performance `json:"performance"`
	Obfs             obfs.Config `json:"obfs"`
	Cover            CoverClient `json:"cover"`
	UDP              ClientUDP   `json:"udp"`
}

type ClientUDP struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	Bind            string `json:"bind"`
	IdleTimeout     string `json:"idle_timeout"`
	MaxDatagramSize int    `json:"max_datagram_size"`
	QueuePackets    int    `json:"queue_packets"`
}

func boolValue(value bool) *bool { return &value }

func (u ClientUDP) IsEnabled() bool { return u.Enabled == nil || *u.Enabled }
func (u ServerUDP) IsEnabled() bool { return u.Enabled == nil || *u.Enabled }

func Load(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(v); err != nil {
		return fmt.Errorf("decode config %s: %w", path, err)
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode config %s: trailing data", path)
	}
	return nil
}

func (s *Server) Defaults() {
	if s.Listen == "" {
		s.Listen = "0.0.0.0:8443"
	}
	if s.HandshakeTimeout == "" {
		s.HandshakeTimeout = "10s"
	}
	if s.DialTimeout == "" {
		s.DialTimeout = "10s"
	}
	if s.IdleTimeout == "" {
		s.IdleTimeout = "5m"
	}
	if s.LogSize == "" {
		s.LogSize = "0M"
	}
	if s.LogFile == "" {
		s.LogFile = "logs/server.log"
	}
	s.Performance.Defaults()
	if s.Obfs.Provider == "" {
		s.Obfs.Provider = "none"
	}
	if s.Cover.Enabled {
		if s.Cover.MaxRateKbpsPerUser == 0 {
			s.Cover.MaxRateKbpsPerUser = 8000
		}
		if s.Cover.MaxSessionsPerUser == 0 {
			s.Cover.MaxSessionsPerUser = coverMaxSessions
		}
	}
	if s.UDP.Enabled == nil {
		s.UDP.Enabled = boolValue(true)
	}
	if s.UDP.IdleTimeout == "" {
		s.UDP.IdleTimeout = "2m"
	}
	if s.UDP.MaxDatagramSize == 0 {
		s.UDP.MaxDatagramSize = 60 << 10
	}
	if s.UDP.QueuePackets == 0 {
		s.UDP.QueuePackets = 256
	}
	if s.UDP.MaxAssociationsPerUser == 0 {
		s.UDP.MaxAssociationsPerUser = 256
	}
	if s.UDP.MaxDestinationsPerAssociation == 0 {
		s.UDP.MaxDestinationsPerAssociation = 1024
	}
	if s.UDP.PacketsPerSecondPerUser == 0 {
		s.UDP.PacketsPerSecondPerUser = 10000
	}
	if s.UDP.BytesPerSecondPerUser == 0 {
		s.UDP.BytesPerSecondPerUser = 64 << 20
	}
	if s.UDP.DNSCacheTTL == "" {
		s.UDP.DNSCacheTTL = "30s"
	}
	if s.UDP.DNSNegativeTTL == "" {
		s.UDP.DNSNegativeTTL = "5s"
	}
	if s.Outbound.Enabled() && s.Outbound.DNS == "" {
		s.Outbound.DNS = "local"
	}
	for i := range s.Users {
		if s.Users[i].MaxConnections <= 0 {
			s.Users[i].MaxConnections = 4096
		}
	}
}

func (c *Client) Defaults() {
	if c.SocksListen == "" {
		c.SocksListen = "127.0.0.1:8444"
	}
	if c.ConnectTimeout == "" {
		c.ConnectTimeout = "10s"
	}
	if c.HandshakeTimeout == "" {
		c.HandshakeTimeout = "10s"
	}
	if c.IdleTimeout == "" {
		c.IdleTimeout = "5m"
	}
	if c.LogSize == "" {
		c.LogSize = "0M"
	}
	if c.LogFile == "" {
		c.LogFile = "logs/client.log"
	}
	c.Performance.Defaults()
	if c.Obfs.Provider == "" {
		c.Obfs.Provider = "none"
	}
	if c.Cover.Enabled && c.Cover.RateKbps == 0 {
		c.Cover.RateKbps = 1000
	}
	if c.UDP.Enabled == nil {
		c.UDP.Enabled = boolValue(true)
	}
	if c.UDP.Bind == "" {
		host, _, err := net.SplitHostPort(c.SocksListen)
		if err == nil && host != "" {
			c.UDP.Bind = net.JoinHostPort(host, "0")
		} else {
			c.UDP.Bind = "127.0.0.1:0"
		}
	}
	if c.UDP.IdleTimeout == "" {
		c.UDP.IdleTimeout = "2m"
	}
	if c.UDP.MaxDatagramSize == 0 {
		c.UDP.MaxDatagramSize = 60 << 10
	}
	if c.UDP.QueuePackets == 0 {
		c.UDP.QueuePackets = 256
	}
}

func (s Server) Validate() error {
	if s.Listen == "" {
		return errors.New("listen is required")
	}
	if _, err := Duration(s.HandshakeTimeout); err != nil {
		return fmt.Errorf("handshake_timeout: %w", err)
	}
	if _, err := Duration(s.DialTimeout); err != nil {
		return fmt.Errorf("dial_timeout: %w", err)
	}
	if _, err := Duration(s.IdleTimeout); err != nil {
		return fmt.Errorf("idle_timeout: %w", err)
	}
	if err := validateObfs(s.Obfs); err != nil {
		return err
	}
	if err := validateCoverServer(s.Cover); err != nil {
		return err
	}
	if err := s.Performance.Validate(); err != nil {
		return err
	}
	if err := validateServerUDP(s.UDP); err != nil {
		return err
	}
	if err := validateOutbound(s.Outbound); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(s.Users))
	for _, user := range s.Users {
		if user.ClientID == "" {
			return errors.New("users[].client_id is required")
		}
		if _, ok := seen[user.ClientID]; ok {
			return fmt.Errorf("duplicate client_id %q", user.ClientID)
		}
		seen[user.ClientID] = struct{}{}
		if _, err := Secret(user.Secret); err != nil {
			return fmt.Errorf("user %q: %w", user.ClientID, err)
		}
	}
	return nil
}

func validateOutbound(value Outbound) error {
	if !value.Enabled() {
		return nil
	}
	if value.Type != "socks5" {
		return errors.New("outbound.type must be socks5")
	}
	host, port, err := net.SplitHostPort(value.Address)
	if err != nil || host == "" {
		return errors.New("outbound.address must be a valid host:port")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return errors.New("outbound.address must contain a numeric port from 1 to 65535")
	}
	if value.DNS != "local" && value.DNS != "remote" {
		return errors.New("outbound.dns must be local or remote")
	}
	if value.Password != "" && value.Username == "" {
		return errors.New("outbound.username is required when password is configured")
	}
	if value.Username != "" && value.Password == "" {
		return errors.New("outbound.password is required when username is configured")
	}
	if len(value.Username) > 255 || len(value.Password) > 255 {
		return errors.New("outbound SOCKS5 username and password must not exceed 255 bytes")
	}
	return nil
}

func (c Client) Validate() error {
	if c.Server == "" {
		return errors.New("server is required")
	}
	if c.ClientID == "" {
		return errors.New("client_id is required")
	}
	if _, err := Secret(c.Secret); err != nil {
		return err
	}
	if err := validateObfs(c.Obfs); err != nil {
		return err
	}
	if err := validateCoverClient(c.Cover); err != nil {
		return err
	}
	if err := c.Performance.Validate(); err != nil {
		return err
	}
	if err := validateClientUDP(c.UDP); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"connect_timeout": c.ConnectTimeout, "handshake_timeout": c.HandshakeTimeout,
		"idle_timeout": c.IdleTimeout,
	} {
		if _, err := Duration(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func (p *Performance) Defaults() {
	if p.Mode == "" {
		p.Mode = "interactive"
	}
}

func (p Performance) Validate() error {
	switch p.Mode {
	case "interactive", "throughput":
		return nil
	default:
		return errors.New("performance.mode must be interactive or throughput")
	}
}

func validateClientUDP(value ClientUDP) error {
	if _, err := Duration(value.IdleTimeout); err != nil {
		return fmt.Errorf("udp.idle_timeout: %w", err)
	}
	host, port, err := net.SplitHostPort(value.Bind)
	if err != nil || host == "" || port != "0" || net.ParseIP(host) == nil {
		return errors.New("udp.bind must be an IP address with port 0")
	}
	if value.MaxDatagramSize < 512 || value.MaxDatagramSize > 60<<10 {
		return errors.New("udp.max_datagram_size must be between 512 and 61440")
	}
	if value.QueuePackets < 1 || value.QueuePackets > 4096 {
		return errors.New("udp.queue_packets must be between 1 and 4096")
	}
	return nil
}

func validateServerUDP(value ServerUDP) error {
	if _, err := Duration(value.IdleTimeout); err != nil {
		return fmt.Errorf("udp.idle_timeout: %w", err)
	}
	if value.MaxDatagramSize < 512 || value.MaxDatagramSize > 60<<10 {
		return errors.New("udp.max_datagram_size must be between 512 and 61440")
	}
	if value.QueuePackets < 1 || value.QueuePackets > 4096 {
		return errors.New("udp.queue_packets must be between 1 and 4096")
	}
	if value.MaxAssociationsPerUser < 1 || value.MaxAssociationsPerUser > 65535 {
		return errors.New("udp.max_associations_per_user must be between 1 and 65535")
	}
	if value.MaxDestinationsPerAssociation < 1 || value.MaxDestinationsPerAssociation > 65535 {
		return errors.New("udp.max_destinations_per_association must be between 1 and 65535")
	}
	if value.PacketsPerSecondPerUser < 1 || value.BytesPerSecondPerUser < 1 {
		return errors.New("UDP per-user rate limits must be positive")
	}
	if _, err := Duration(value.DNSCacheTTL); err != nil {
		return fmt.Errorf("udp.dns_cache_ttl: %w", err)
	}
	if _, err := Duration(value.DNSNegativeTTL); err != nil {
		return fmt.Errorf("udp.dns_negative_ttl: %w", err)
	}
	return nil
}

// Cover-traffic bounds shared by client and server validation. coverMaxSessions
// matches the hard cap the client enforces on mock connections.
const (
	coverMinRateKbps = 64
	coverMaxRateKbps = 100000
	coverMaxSessions = 32
)

func validateCoverClient(value CoverClient) error {
	if !value.Enabled {
		return nil
	}
	if value.RateKbps < coverMinRateKbps || value.RateKbps > coverMaxRateKbps {
		return fmt.Errorf("cover.rate_kbps must be between %d and %d", coverMinRateKbps, coverMaxRateKbps)
	}
	return nil
}

func validateCoverServer(value CoverServer) error {
	if !value.Enabled {
		return nil
	}
	if value.MaxRateKbpsPerUser < coverMinRateKbps || value.MaxRateKbpsPerUser > coverMaxRateKbps {
		return fmt.Errorf("cover.max_rate_kbps_per_user must be between %d and %d", coverMinRateKbps, coverMaxRateKbps)
	}
	if value.MaxSessionsPerUser < 1 || value.MaxSessionsPerUser > coverMaxSessions {
		return fmt.Errorf("cover.max_sessions_per_user must be between 1 and %d", coverMaxSessions)
	}
	if value.MaxTotalRateMbps < 0 {
		return errors.New("cover.max_total_rate_mbps must not be negative")
	}
	return nil
}

func validateObfs(value obfs.Config) error {
	if math.IsNaN(value.Participation) || math.IsInf(value.Participation, 0) ||
		value.Participation < 0 || value.Participation > 1 {
		return errors.New("obfs.participation must be a finite number between 0 and 1")
	}
	return nil
}

func Secret(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errors.New("secret must be unpadded base64url")
	}
	if len(b) < 32 {
		return nil, errors.New("secret must be at least 32 bytes")
	}
	return b, nil
}

func Duration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid positive duration %q", s)
	}
	return d, nil
}
