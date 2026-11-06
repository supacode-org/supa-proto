package protocol

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"crypto/hkdf"
)

const (
	Version        = 4
	MaxLine        = 96 << 10
	MaxPayload     = 64 << 10
	MaxUDPDatagram = 60 << 10
)

const (
	FrameOpen          byte = 1
	FrameOpenResult    byte = 2
	FrameData          byte = 3
	FrameCloseWrite    byte = 4
	FrameReset         byte = 5
	FramePing          byte = 6
	FramePong          byte = 7
	FrameKeyConfirm    byte = 8
	FrameUDPOpen       byte = 9
	FrameUDPOpenResult byte = 10
	FrameUDPDatagram   byte = 11
	FrameUDPClose      byte = 12
	// Cover-traffic frames. A cover session completes the normal handshake and
	// key confirmation, then sends COVER_OPEN in place of OPEN. It never
	// carries a real target: both ends generate or discard DATA frames to
	// produce decoy traffic. Servers that predate these types reject the
	// unknown frame, which lets a cover-capable client detect and stop.
	FrameCoverOpen       byte = 13
	FrameCoverOpenResult byte = 14
)

type Envelope struct {
	V        int     `json:"v"`
	Kind     string  `json:"kind"`
	ClientID string  `json:"client_id,omitempty"`
	TS       int64   `json:"ts,omitempty"`
	CN       string  `json:"cn,omitempty"`
	CE       string  `json:"ce,omitempty"`
	OTP      string  `json:"otp,omitempty"`
	MAC      string  `json:"mac,omitempty"`
	SID      string  `json:"sid,omitempty"`
	SN       string  `json:"sn,omitempty"`
	SE       string  `json:"se,omitempty"`
	Exp      int64   `json:"exp,omitempty"`
	Dir      string  `json:"dir,omitempty"`
	Seq      *uint64 `json:"seq,omitempty"`
	Data     string  `json:"data,omitempty"`
	Code     string  `json:"code,omitempty"`
	Message  string  `json:"message,omitempty"`
}

type Frame struct {
	Type    byte
	Flags   byte
	Payload []byte
}

var linePool = sync.Pool{
	New: func() any { return make([]byte, 0, MaxLine) },
}

func B64(b []byte) string            { return base64.RawURLEncoding.EncodeToString(b) }
func UnB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func Random(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func WriteLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxLine {
		return errors.New("protocol line too large")
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func ReadLine(r *bufio.Reader) (Envelope, error) {
	line := linePool.Get().([]byte)[:0]
	defer linePool.Put(line[:0])
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > MaxLine {
			return Envelope{}, errors.New("protocol line too large")
		}
		line = append(line, part...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return Envelope{}, err
		}
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if err := rejectDuplicateKeys(line); err != nil {
		return Envelope{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return Envelope{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Envelope{}, errors.New("trailing JSON data")
	}
	if env.V != Version {
		return Envelope{}, errors.New("unsupported protocol version")
	}
	return env, nil
}

func rejectDuplicateKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('{') {
		return errors.New("protocol message must be an object")
	}
	seen := map[string]struct{}{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return errors.New("invalid object key")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON key %q", key)
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func lpWrite(w io.Writer, fields ...[]byte) {
	var n [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(n[:], uint32(len(field)))
		_, _ = w.Write(n[:])
		_, _ = w.Write(field)
	}
}

func int64Bytes(v int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return b[:]
}

func keyedMAC(key []byte, fields ...[]byte) string {
	h := hmac.New(sha256.New, key)
	lpWrite(h, fields...)
	return B64(h.Sum(nil))
}

func clientMAC(root, providerIdentity []byte, hello Envelope) string {
	cn, _ := UnB64(hello.CN)
	ce, _ := UnB64(hello.CE)
	return keyedMAC(root, []byte("SupaProto/4 client hello"), []byte{Version}, providerIdentity,
		[]byte(hello.ClientID), int64Bytes(hello.TS), cn, ce, []byte(hello.OTP))
}

func serverMAC(root, providerIdentity []byte, clientHello, serverHello Envelope) string {
	cn, _ := UnB64(clientHello.CN)
	ce, _ := UnB64(clientHello.CE)
	sid, _ := UnB64(serverHello.SID)
	sn, _ := UnB64(serverHello.SN)
	se, _ := UnB64(serverHello.SE)
	return keyedMAC(root, []byte("SupaProto/4 server hello"), []byte{Version}, providerIdentity,
		[]byte(clientHello.ClientID), int64Bytes(clientHello.TS), cn, ce,
		[]byte(clientHello.OTP), sid, sn, se, int64Bytes(serverHello.Exp))
}

func EqualMAC(want, got string) bool {
	a, errA := UnB64(want)
	b, errB := UnB64(got)
	return errA == nil && errB == nil && hmac.Equal(a, b)
}

func TOTP(root []byte, at time.Time) (string, error) {
	key, err := hkdf.Key(sha256.New, root, []byte("SupaProto/4 otp"), "SupaProto/4 TOTP", 32)
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(counter[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 15
	n := (uint32(sum[offset])&127)<<24 | uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%08d", n%100000000), nil
}

func ValidTOTP(root []byte, otp string, now time.Time) bool {
	if len(otp) != 8 {
		return false
	}
	for _, delta := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		value, err := TOTP(root, now.Add(delta))
		if err == nil && hmac.Equal([]byte(value), []byte(otp)) {
			return true
		}
	}
	return false
}

func NewClientHello(root, providerIdentity []byte, clientID string, now time.Time) (Envelope, *ecdh.PrivateKey, error) {
	if clientID == "" {
		return Envelope{}, nil, errors.New("client_id is required")
	}
	cn, err := Random(32)
	if err != nil {
		return Envelope{}, nil, err
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, nil, err
	}
	otp, err := TOTP(root, now)
	if err != nil {
		return Envelope{}, nil, err
	}
	hello := Envelope{
		V: Version, Kind: "client_hello", ClientID: clientID, TS: now.Unix(),
		CN: B64(cn), CE: B64(privateKey.PublicKey().Bytes()), OTP: otp,
	}
	hello.MAC = clientMAC(root, providerIdentity, hello)
	return hello, privateKey, nil
}

func ValidateClientHello(hello Envelope, root, providerIdentity []byte, now time.Time, skew time.Duration) error {
	if hello.Kind != "client_hello" || hello.ClientID == "" || hello.Seq != nil || hello.Data != "" ||
		hello.SID != "" || hello.SN != "" || hello.SE != "" || hello.Exp != 0 || hello.Dir != "" ||
		hello.Code != "" || hello.Message != "" {
		return errors.New("invalid client hello")
	}
	cn, err := UnB64(hello.CN)
	if err != nil || len(cn) != 32 {
		return errors.New("invalid client nonce")
	}
	ce, err := UnB64(hello.CE)
	if err != nil || len(ce) != 32 {
		return errors.New("invalid client ephemeral key")
	}
	if _, err = ecdh.X25519().NewPublicKey(ce); err != nil {
		return errors.New("invalid client ephemeral key")
	}
	delta := now.Sub(time.Unix(hello.TS, 0))
	if delta < -skew || delta > skew {
		return errors.New("client timestamp outside allowed window")
	}
	if !ValidTOTP(root, hello.OTP, now) || !EqualMAC(clientMAC(root, providerIdentity, hello), hello.MAC) {
		return errors.New("client authentication failed")
	}
	return nil
}

func NewServerHello(root, providerIdentity []byte, clientHello Envelope, now time.Time, lifetime time.Duration) (Envelope, *ecdh.PrivateKey, error) {
	sid, err := Random(16)
	if err != nil {
		return Envelope{}, nil, err
	}
	sn, err := Random(32)
	if err != nil {
		return Envelope{}, nil, err
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, nil, err
	}
	hello := Envelope{
		V: Version, Kind: "server_hello", SID: B64(sid), SN: B64(sn),
		SE: B64(privateKey.PublicKey().Bytes()), Exp: now.Add(lifetime).Unix(),
	}
	hello.MAC = serverMAC(root, providerIdentity, clientHello, hello)
	return hello, privateKey, nil
}

func ValidateServerHello(serverHello, clientHello Envelope, root, providerIdentity []byte, now time.Time) error {
	if serverHello.Kind != "server_hello" || serverHello.Exp <= now.Unix() ||
		serverHello.ClientID != "" || serverHello.TS != 0 || serverHello.CN != "" ||
		serverHello.CE != "" || serverHello.OTP != "" || serverHello.Seq != nil ||
		serverHello.Data != "" || serverHello.Dir != "" || serverHello.Code != "" ||
		serverHello.Message != "" {
		return errors.New("invalid or expired server hello")
	}
	for name, encoded := range map[string]struct {
		value string
		size  int
	}{
		"session id":           {serverHello.SID, 16},
		"server nonce":         {serverHello.SN, 32},
		"server ephemeral key": {serverHello.SE, 32},
	} {
		value, err := UnB64(encoded.value)
		if err != nil || len(value) != encoded.size {
			return fmt.Errorf("invalid %s", name)
		}
	}
	se, _ := UnB64(serverHello.SE)
	if _, err := ecdh.X25519().NewPublicKey(se); err != nil {
		return errors.New("invalid server ephemeral key")
	}
	if !EqualMAC(serverMAC(root, providerIdentity, clientHello, serverHello), serverHello.MAC) {
		return errors.New("server authentication failed")
	}
	return nil
}

func transcriptHash(clientHello, serverHello Envelope, providerIdentity []byte) ([]byte, error) {
	decode := func(value string) ([]byte, error) { return UnB64(value) }
	cn, err := decode(clientHello.CN)
	if err != nil {
		return nil, err
	}
	ce, err := decode(clientHello.CE)
	if err != nil {
		return nil, err
	}
	sid, err := decode(serverHello.SID)
	if err != nil {
		return nil, err
	}
	sn, err := decode(serverHello.SN)
	if err != nil {
		return nil, err
	}
	se, err := decode(serverHello.SE)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	lpWrite(h, []byte("SupaProto/4 transcript"), []byte{Version}, providerIdentity,
		[]byte(clientHello.ClientID), int64Bytes(clientHello.TS), cn, ce,
		[]byte(clientHello.OTP), sid, sn, se, int64Bytes(serverHello.Exp))
	return h.Sum(nil), nil
}

type Session struct {
	sid                    []byte
	sendAEAD, receiveAEAD  cipher.AEAD
	sendPrefix, recvPrefix [4]byte
	sendDir, recvDir       string
	sendSeq, recvSeq       uint64
	confirm, transcript    []byte
	sendMu, receiveMu      sync.Mutex
}

func NewSession(root, providerIdentity []byte, clientHello, serverHello Envelope, privateKey *ecdh.PrivateKey, sendDir string) (*Session, error) {
	var peerEncoded string
	switch sendDir {
	case "c2s":
		peerEncoded = serverHello.SE
	case "s2c":
		peerEncoded = clientHello.CE
	default:
		return nil, errors.New("invalid session direction")
	}
	peerBytes, err := UnB64(peerEncoded)
	if err != nil {
		return nil, err
	}
	peer, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil {
		return nil, err
	}
	shared, err := privateKey.ECDH(peer)
	if err != nil {
		return nil, err
	}
	th, err := transcriptHash(clientHello, serverHello, providerIdentity)
	if err != nil {
		return nil, err
	}
	ikm := make([]byte, 0, len(shared)+len(root))
	ikm = append(ikm, shared...)
	ikm = append(ikm, root...)
	prk, err := hkdf.Extract(sha256.New, ikm, th)
	if err != nil {
		return nil, err
	}
	sid, err := UnB64(serverHello.SID)
	if err != nil {
		return nil, err
	}
	expand := func(label string, n int) ([]byte, error) {
		return hkdf.Expand(sha256.New, prk, label+B64(sid), n)
	}
	c2sKey, err := expand("SupaProto/4 c2s key", 32)
	if err != nil {
		return nil, err
	}
	s2cKey, err := expand("SupaProto/4 s2c key", 32)
	if err != nil {
		return nil, err
	}
	c2sNonce, err := expand("SupaProto/4 c2s nonce", 4)
	if err != nil {
		return nil, err
	}
	s2cNonce, err := expand("SupaProto/4 s2c nonce", 4)
	if err != nil {
		return nil, err
	}
	confirm, err := expand("SupaProto/4 confirm", 32)
	if err != nil {
		return nil, err
	}
	newAEAD := func(key []byte) (cipher.AEAD, error) {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	}
	c2sAEAD, err := newAEAD(c2sKey)
	if err != nil {
		return nil, err
	}
	s2cAEAD, err := newAEAD(s2cKey)
	if err != nil {
		return nil, err
	}
	session := &Session{sid: sid, confirm: confirm, transcript: th, sendDir: sendDir}
	if sendDir == "c2s" {
		session.recvDir, session.sendAEAD, session.receiveAEAD = "s2c", c2sAEAD, s2cAEAD
		copy(session.sendPrefix[:], c2sNonce)
		copy(session.recvPrefix[:], s2cNonce)
	} else {
		session.recvDir, session.sendAEAD, session.receiveAEAD = "c2s", s2cAEAD, c2sAEAD
		copy(session.sendPrefix[:], s2cNonce)
		copy(session.recvPrefix[:], c2sNonce)
	}
	return session, nil
}

func (s *Session) ID() string { return B64(s.sid) }

func (s *Session) Confirmation(role string) []byte {
	h := hmac.New(sha256.New, s.confirm)
	lpWrite(h, []byte(role), s.transcript)
	return h.Sum(nil)
}

func (s *Session) VerifyConfirmation(role string, got []byte) bool {
	return hmac.Equal(s.Confirmation(role), got)
}

func makeNonce(prefix [4]byte, seq uint64) []byte {
	b := make([]byte, 12)
	copy(b, prefix[:])
	binary.BigEndian.PutUint64(b[4:], seq)
	return b
}

func frameAAD(sid []byte, direction string, seq uint64) []byte {
	var b bytes.Buffer
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], seq)
	lpWrite(&b, []byte("SupaProto/4 frame"), []byte{Version}, []byte("frame"),
		sid, []byte(direction), n[:])
	return b.Bytes()
}

func EncodeFrame(frame Frame) ([]byte, error) {
	if len(frame.Payload) > MaxPayload {
		return nil, errors.New("frame payload too large")
	}
	b := make([]byte, 7+len(frame.Payload))
	b[0], b[1], b[2] = Version, frame.Type, frame.Flags
	binary.BigEndian.PutUint32(b[3:7], uint32(len(frame.Payload)))
	copy(b[7:], frame.Payload)
	return b, nil
}

func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < 7 || b[0] != Version || b[2] != 0 ||
		int(binary.BigEndian.Uint32(b[3:7])) != len(b)-7 {
		return Frame{}, errors.New("invalid inner frame")
	}
	switch b[1] {
	case FrameOpen, FrameOpenResult, FrameData, FrameCloseWrite, FrameReset,
		FramePing, FramePong, FrameKeyConfirm, FrameUDPOpen, FrameUDPOpenResult,
		FrameUDPDatagram, FrameUDPClose, FrameCoverOpen, FrameCoverOpenResult:
	default:
		return Frame{}, errors.New("unknown frame type")
	}
	payloadLength := len(b) - 7
	switch b[1] {
	case FrameData:
		if payloadLength == 0 {
			return Frame{}, errors.New("empty DATA frame")
		}
	case FrameCloseWrite:
		if payloadLength != 0 {
			return Frame{}, errors.New("invalid CLOSE_WRITE frame")
		}
	case FramePing, FramePong:
		if payloadLength != 8 {
			return Frame{}, errors.New("invalid PING/PONG frame")
		}
	case FrameKeyConfirm:
		if payloadLength != sha256.Size {
			return Frame{}, errors.New("invalid KEY_CONFIRM frame")
		}
	case FrameUDPOpen:
		if payloadLength != 2 || b[7] != 1 || b[8] != 0 {
			return Frame{}, errors.New("invalid UDP_OPEN frame")
		}
	case FrameCoverOpen:
		if payloadLength != 3 || b[7] != 1 {
			return Frame{}, errors.New("invalid COVER_OPEN frame")
		}
	case FrameUDPOpenResult, FrameUDPClose:
		if payloadLength == 0 || payloadLength > 256 {
			return Frame{}, errors.New("invalid UDP result frame")
		}
	case FrameCoverOpenResult:
		if payloadLength == 0 || payloadLength > 256 {
			return Frame{}, errors.New("invalid COVER_OPEN_RESULT frame")
		}
	case FrameUDPDatagram:
		if _, _, _, err := ParseUDPDatagram(b[7:]); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: b[1], Flags: b[2], Payload: b[7:]}, nil
}

func UDPOpenPayload() []byte { return []byte{1, 0} }

// CoverOpenPayload encodes a cover-session open. The byte is a version marker
// and the uint16 is the per-session target wire rate in kbps, which the server
// uses to pace generated cover data (and clamps to its own limit).
func CoverOpenPayload(rateKbps uint16) []byte {
	b := []byte{1, 0, 0}
	binary.BigEndian.PutUint16(b[1:], rateKbps)
	return b
}

// ParseCoverOpen returns the target rate in kbps from a COVER_OPEN payload.
func ParseCoverOpen(payload []byte) (uint16, error) {
	if len(payload) != 3 || payload[0] != 1 {
		return 0, errors.New("invalid COVER_OPEN")
	}
	return binary.BigEndian.Uint16(payload[1:]), nil
}

// UDPDatagramPayload encodes one complete UDP datagram. The address is a
// destination in c2s frames and the observed source in s2c frames.
func UDPDatagramPayload(host string, port uint16, data []byte) ([]byte, error) {
	if len(data) > MaxUDPDatagram {
		return nil, errors.New("UDP datagram too large")
	}
	address, err := OpenPayload(host, port)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(address)+2+len(data))
	result = append(result, address...)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(data)))
	result = append(result, size[:]...)
	result = append(result, data...)
	if len(result) > MaxPayload {
		return nil, errors.New("UDP frame payload too large")
	}
	return result, nil
}

func ParseUDPDatagram(payload []byte) (string, uint16, []byte, error) {
	addressLength, err := encodedAddressLength(payload)
	if err != nil || len(payload) < addressLength+2 {
		return "", 0, nil, errors.New("invalid UDP_DATAGRAM address")
	}
	host, port, err := ParseOpen(payload[:addressLength])
	if err != nil {
		return "", 0, nil, err
	}
	dataLength := int(binary.BigEndian.Uint16(payload[addressLength : addressLength+2]))
	if dataLength > MaxUDPDatagram || len(payload) != addressLength+2+dataLength {
		return "", 0, nil, errors.New("invalid UDP_DATAGRAM length")
	}
	return host, port, payload[addressLength+2:], nil
}

func encodedAddressLength(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, errors.New("missing address")
	}
	switch payload[0] {
	case 1:
		if len(payload) < 7 {
			return 0, errors.New("short IPv4 address")
		}
		return 7, nil
	case 4:
		if len(payload) < 19 {
			return 0, errors.New("short IPv6 address")
		}
		return 19, nil
	case 3:
		if len(payload) < 2 || payload[1] == 0 || payload[1] > 253 {
			return 0, errors.New("invalid domain address")
		}
		n := int(payload[1]) + 4
		if len(payload) < n {
			return 0, errors.New("short domain address")
		}
		return n, nil
	default:
		return 0, errors.New("unsupported address type")
	}
}

func (s *Session) Seal(frame Frame) (Envelope, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	plain, err := EncodeFrame(frame)
	if err != nil {
		return Envelope{}, err
	}
	seq := s.sendSeq
	data := s.sendAEAD.Seal(nil, makeNonce(s.sendPrefix, seq), plain,
		frameAAD(s.sid, s.sendDir, seq))
	s.sendSeq++
	return Envelope{
		V: Version, Kind: "frame", SID: B64(s.sid), Dir: s.sendDir,
		Seq: &seq, Data: B64(data),
	}, nil
}

func (s *Session) Open(env Envelope) (Frame, error) {
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()
	if env.Kind != "frame" || env.SID != B64(s.sid) || env.Dir != s.recvDir ||
		env.Seq == nil || *env.Seq != s.recvSeq || env.Data == "" ||
		env.ClientID != "" || env.TS != 0 || env.CN != "" || env.CE != "" ||
		env.OTP != "" || env.MAC != "" || env.SN != "" || env.SE != "" || env.Exp != 0 ||
		env.Code != "" || env.Message != "" {
		return Frame{}, errors.New("invalid frame envelope")
	}
	data, err := UnB64(env.Data)
	if err != nil {
		return Frame{}, err
	}
	seq := s.recvSeq
	plain, err := s.receiveAEAD.Open(nil, makeNonce(s.recvPrefix, seq), data,
		frameAAD(s.sid, env.Dir, seq))
	if err != nil {
		return Frame{}, errors.New("frame authentication failed")
	}
	frame, err := DecodeFrame(plain)
	if err != nil {
		return Frame{}, err
	}
	s.recvSeq++
	return frame, nil
}

func OpenPayload(host string, port uint16) ([]byte, error) {
	if port == 0 {
		return nil, errors.New("invalid target port")
	}
	ip := net.ParseIP(host)
	var b []byte
	switch {
	case ip != nil && ip.To4() != nil:
		b = append([]byte{1}, ip.To4()...)
	case ip != nil && ip.To16() != nil:
		b = append([]byte{4}, ip.To16()...)
	default:
		if len(host) == 0 || len(host) > 253 {
			return nil, errors.New("invalid target host")
		}
		b = append([]byte{3, byte(len(host))}, []byte(host)...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	return append(b, p[:]...), nil
}

func ParseOpen(b []byte) (string, uint16, error) {
	if len(b) < 3 {
		return "", 0, errors.New("invalid OPEN")
	}
	var host string
	switch b[0] {
	case 1:
		if len(b) != 7 {
			return "", 0, errors.New("invalid IPv4 OPEN")
		}
		host = net.IP(b[1:5]).String()
	case 4:
		if len(b) != 19 {
			return "", 0, errors.New("invalid IPv6 OPEN")
		}
		host = net.IP(b[1:17]).String()
	case 3:
		n := int(b[1])
		if n == 0 || len(b) != n+4 {
			return "", 0, errors.New("invalid domain OPEN")
		}
		host = string(b[2 : 2+n])
	default:
		return "", 0, errors.New("unsupported address type")
	}
	port := binary.BigEndian.Uint16(b[len(b)-2:])
	if port == 0 {
		return "", 0, errors.New("invalid target port")
	}
	return host, port, nil
}

func Result(code byte, message string) []byte {
	if len(message) > 255 {
		message = message[:255]
	}
	return append([]byte{code}, []byte(message)...)
}

func ParseResult(b []byte) (byte, string, error) {
	if len(b) == 0 {
		return 0, "", errors.New("empty result")
	}
	return b[0], string(b[1:]), nil
}

func ParsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	return uint16(n), err
}
