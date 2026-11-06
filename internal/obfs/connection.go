package obfs

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supaproto/internal/protocol"
)

type wireField struct {
	name string
	raw  json.RawMessage
}

const wireReadBuffer = 16 << 10

type Conn struct {
	net.Conn
	provider  *Provider
	reader    *bufio.Reader
	sendDir   string
	recvDir   string
	phaseMu   sync.RWMutex
	phase     string
	writeMu   sync.Mutex
	done      chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	lastReal  atomic.Int64

	budgetMu       sync.Mutex
	decoyTimes     []time.Time
	realBytes      int64
	decoyBytes     int64
	preAuthDecoys  int
	consecutiveIn  int
	sentDecoys     atomic.Uint64
	receivedDecoys atomic.Uint64
}

func (p *Provider) NewConn(conn net.Conn, sendDir string) (*Conn, error) {
	if sendDir != "c2s" && sendDir != "s2c" {
		return nil, errors.New("invalid obfs connection direction")
	}
	recvDir := "s2c"
	if sendDir == "s2c" {
		recvDir = "c2s"
	}
	c := &Conn{
		Conn: conn, provider: p, reader: bufio.NewReaderSize(conn, wireReadBuffer),
		sendDir: sendDir, recvDir: recvDir, phase: "before_hello", done: make(chan struct{}),
	}
	c.lastReal.Store(time.Now().UnixNano())
	return c, nil
}

func (c *Conn) Provider() *Provider { return c.provider }

func (c *Conn) SetPhase(phase string) {
	c.phaseMu.Lock()
	c.phase = phase
	c.phaseMu.Unlock()
}

func (c *Conn) Phase() string {
	c.phaseMu.RLock()
	defer c.phaseMu.RUnlock()
	return c.phase
}

func (c *Conn) Stop() {
	c.stopOnce.Do(func() { close(c.done) })
}

func (c *Conn) Close() error {
	c.Stop()
	return c.Conn.Close()
}

func (c *Conn) EmitPreHello() {
	if c.provider.ParticipationMilli == 0 {
		return
	}
	for i := range c.provider.Descriptor.Decoys {
		rule := &c.provider.Descriptor.Decoys[i]
		if rule.ResponseOnly || !directionsContain(rule.Directions, c.sendDir) ||
			!phasesContain(rule.Phases, "before_hello") {
			continue
		}
		if chance(rule.BaseProbability * c.provider.Participation) {
			_ = c.writeDecoy(rule, nil)
			return
		}
	}
}

func (c *Conn) StartScheduler() {
	if c.provider.ParticipationMilli == 0 {
		return
	}
	c.startOnce.Do(func() { go c.scheduler() })
}

func (c *Conn) scheduler() {
	next := make(map[string]time.Time)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return
		case now := <-timer.C:
			phase := c.Phase()
			for i := range c.provider.Descriptor.Decoys {
				rule := &c.provider.Descriptor.Decoys[i]
				if rule.ResponseOnly || rule.Trigger.Type != "idle_interval" ||
					!directionsContain(rule.Directions, c.sendDir) ||
					(!phasesContain(rule.Phases, phase) && !phasesContain(rule.Phases, "established")) {
					continue
				}
				when := next[rule.ID]
				if when.IsZero() {
					next[rule.ID] = now.Add(c.decoyInterval(rule))
					continue
				}
				if now.Before(when) {
					continue
				}
				next[rule.ID] = now.Add(c.decoyInterval(rule))
				if chance(rule.BaseProbability * c.provider.Participation) {
					_ = c.writeDecoy(rule, nil)
				}
			}
		}
	}
}

func (c *Conn) decoyInterval(rule *DecoyRule) time.Duration {
	minimum, maximum := rule.Trigger.MinMS, rule.Trigger.MaxMS
	if maximum <= 0 {
		maximum = 60000
	}
	if minimum < 0 || minimum > maximum {
		minimum = maximum
	}
	effectiveMin := float64(maximum) - c.provider.Participation*float64(maximum-minimum)
	span := float64(maximum) - effectiveMin
	return time.Duration((effectiveMin + randomFloat()*span) * float64(time.Millisecond))
}

func (c *Conn) WriteEnvelope(env protocol.Envelope) error {
	if c.provider.IdentityWireFormat() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if err := protocol.WriteLine(c.Conn, env); err != nil {
			return err
		}
		c.lastReal.Store(time.Now().UnixNano())
		return nil
	}
	fields, err := c.provider.encodeEnvelope(env, c.sendDir)
	if err != nil {
		return err
	}
	line, err := encodeWireObject(fields)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err = c.Conn.Write(line); err != nil {
		return err
	}
	c.budgetMu.Lock()
	c.realBytes += int64(len(line))
	c.budgetMu.Unlock()
	c.lastReal.Store(time.Now().UnixNano())
	return nil
}

func (c *Conn) ReadEnvelope() (protocol.Envelope, error) {
	if c.provider.IdentityWireFormat() {
		env, err := protocol.ReadLine(c.reader)
		if err == nil {
			c.lastReal.Store(time.Now().UnixNano())
		}
		return env, err
	}
	for {
		line, err := readWireLine(c.reader)
		if err != nil {
			return protocol.Envelope{}, err
		}
		env, decoy, raw, err := c.provider.decodeLine(line, c.recvDir)
		if err != nil {
			return protocol.Envelope{}, err
		}
		if decoy == nil {
			c.budgetMu.Lock()
			c.consecutiveIn = 0
			c.budgetMu.Unlock()
			c.lastReal.Store(time.Now().UnixNano())
			return env, nil
		}
		if !phasesContain(decoy.Phases, c.Phase()) {
			return protocol.Envelope{}, errors.New("obfs decoy phase mismatch")
		}
		c.budgetMu.Lock()
		c.consecutiveIn++
		consecutive := c.consecutiveIn
		c.budgetMu.Unlock()
		if consecutive > c.provider.Descriptor.Limits.MaxConsecutiveDecoys {
			return protocol.Envelope{}, errors.New("too many consecutive obfs decoys")
		}
		c.receivedDecoys.Add(1)
		if decoy.Response != nil && chance(decoy.Response.Probability*c.provider.Participation) {
			response := c.provider.decoyByID[decoy.Response.Decoy]
			if response != nil && directionsContain(response.Directions, c.sendDir) {
				delay := randomDuration(decoy.Response.MinDelayMS, decoy.Response.MaxDelayMS)
				request := cloneRawMap(raw)
				go func() {
					select {
					case <-c.done:
					case <-time.After(delay):
						_ = c.writeDecoy(response, request)
					}
				}()
			}
		}
	}
}

func (c *Conn) writeDecoy(rule *DecoyRule, request map[string]json.RawMessage) error {
	if c.provider.ParticipationMilli == 0 || !c.allowDecoy() {
		return nil
	}
	fields := make([]wireField, 0, len(rule.Fields))
	for _, field := range rule.Fields {
		var raw []byte
		var err error
		switch {
		case field.CopyFromRequest != "":
			value, ok := request[field.CopyFromRequest]
			if !ok {
				return errors.New("obfs decoy response source field is missing")
			}
			raw = append([]byte(nil), value...)
		case field.Generator != nil:
			value, genErr := generate(*field.Generator)
			if genErr != nil {
				return genErr
			}
			raw, err = json.Marshal(value)
		default:
			raw, err = json.Marshal(field.Value)
		}
		if err != nil {
			return err
		}
		fields = append(fields, wireField{name: field.Wire, raw: raw})
	}
	line, err := encodeWireObject(fields)
	if err != nil {
		return err
	}
	if len(line) > c.provider.Descriptor.Limits.MaxDecoyBytes {
		return errors.New("obfs decoy exceeds provider byte limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err = c.Conn.Write(line); err != nil {
		return err
	}
	c.budgetMu.Lock()
	c.decoyBytes += int64(len(line))
	c.decoyTimes = append(c.decoyTimes, time.Now())
	if c.Phase() == "before_hello" || c.Phase() == "handshake" {
		c.preAuthDecoys++
	}
	c.budgetMu.Unlock()
	c.sentDecoys.Add(1)
	return nil
}

func (c *Conn) allowDecoy() bool {
	c.budgetMu.Lock()
	defer c.budgetMu.Unlock()
	now := time.Now()
	cutoffMinute := now.Add(-time.Minute)
	kept := c.decoyTimes[:0]
	perSecond := 0
	for _, value := range c.decoyTimes {
		if value.After(cutoffMinute) {
			kept = append(kept, value)
			if value.After(now.Add(-time.Second)) {
				perSecond++
			}
		}
	}
	c.decoyTimes = kept
	l := c.provider.Descriptor.Limits
	if perSecond >= l.MaxDecoysPerSecond || len(kept) >= l.MaxDecoysPerMinute {
		return false
	}
	if (c.Phase() == "before_hello" || c.Phase() == "handshake") && c.preAuthDecoys >= l.MaxPreAuthDecoys {
		return false
	}
	allowedBytes := int64(float64(c.realBytes)*l.MaxOverheadRatio) + 4096
	return c.decoyBytes < allowedBytes
}

func (p *Provider) encodeEnvelope(env protocol.Envelope, direction string) ([]wireField, error) {
	template, ok := p.Descriptor.Messages[env.Kind]
	if !ok {
		return nil, fmt.Errorf("obfs has no template for logical message %q", env.Kind)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	logical := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &logical); err != nil {
		return nil, err
	}
	fields := make([]wireField, 0, len(template.Fields)+len(template.Constants)+4)
	for _, constant := range template.Constants {
		raw, err := json.Marshal(constant.Value)
		if err != nil {
			return nil, err
		}
		fields = append(fields, wireField{name: constant.Wire, raw: raw})
	}
	usedNoise := map[string]bool{}
	extraBytes := 0
	for _, mapping := range template.Fields {
		var raw []byte
		if mapping.Logical == "kind" {
			raw, _ = json.Marshal(p.Descriptor.Discriminator.LogicalValues[env.Kind])
		} else {
			value, exists := logical[mapping.Logical]
			if !exists {
				return nil, fmt.Errorf("logical message %s is missing %s", env.Kind, mapping.Logical)
			}
			raw = value
		}
		fields = append(fields, wireField{name: mapping.Wire, raw: raw})
		for _, slot := range template.NoiseSlots {
			if slot.After != mapping.Wire {
				continue
			}
			for _, id := range slot.Candidates {
				if usedNoise[id] || len(usedNoise) >= p.Descriptor.Limits.MaxExtraFieldsPerMessage {
					continue
				}
				rule := p.Descriptor.NoiseFields[id]
				if !directionsContain(rule.Directions, direction) ||
					!phasesContain(rule.Phases, env.Kind) ||
					!chance(rule.BaseProbability*p.Participation) {
					continue
				}
				value, err := generate(rule.Generator)
				if err != nil {
					return nil, err
				}
				noiseRaw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				added := len(rule.Wire) + len(noiseRaw) + 4
				if extraBytes+added > p.Descriptor.Limits.MaxExtraBytesPerMessage {
					continue
				}
				fields = append(fields, wireField{name: rule.Wire, raw: noiseRaw})
				extraBytes += added
				usedNoise[id] = true
			}
		}
	}
	return fields, nil
}

func (p *Provider) decodeLine(line []byte, direction string) (protocol.Envelope, *DecoyRule, map[string]json.RawMessage, error) {
	raw, err := parseRawObject(line)
	if err != nil {
		return protocol.Envelope{}, nil, nil, err
	}
	discriminatorRaw, ok := raw[p.Descriptor.Discriminator.Field]
	if !ok {
		return protocol.Envelope{}, nil, nil, errors.New("obfs discriminator is missing")
	}
	var discriminator string
	if err = json.Unmarshal(discriminatorRaw, &discriminator); err != nil {
		return protocol.Envelope{}, nil, nil, errors.New("invalid obfs discriminator")
	}
	if decoy := p.decoyByEvent[discriminator]; decoy != nil {
		if !directionsContain(decoy.Directions, direction) {
			return protocol.Envelope{}, nil, nil, errors.New("obfs decoy direction mismatch")
		}
		if err = validateDecoy(raw, decoy); err != nil {
			return protocol.Envelope{}, nil, nil, err
		}
		return protocol.Envelope{}, decoy, raw, nil
	}
	kind := p.wireToKind[discriminator]
	if kind == "" {
		return protocol.Envelope{}, nil, nil, errors.New("unknown obfs discriminator")
	}
	template := p.Descriptor.Messages[kind]
	allowed := map[string]bool{}
	logical := map[string]json.RawMessage{}
	kindRaw, _ := json.Marshal(kind)
	logical["kind"] = kindRaw
	for _, constant := range template.Constants {
		allowed[constant.Wire] = true
		value, exists := raw[constant.Wire]
		expected, _ := json.Marshal(constant.Value)
		if !exists || !jsonEqual(value, expected) {
			return protocol.Envelope{}, nil, nil, errors.New("obfs constant mismatch")
		}
	}
	for _, mapping := range template.Fields {
		allowed[mapping.Wire] = true
		value, exists := raw[mapping.Wire]
		if !exists {
			return protocol.Envelope{}, nil, nil, fmt.Errorf("obfs field %s is missing", mapping.Wire)
		}
		if mapping.Logical != "kind" {
			logical[mapping.Logical] = value
		}
	}
	noiseByWire := map[string]NoiseRule{}
	for _, slot := range template.NoiseSlots {
		for _, id := range slot.Candidates {
			rule := p.Descriptor.NoiseFields[id]
			if directionsContain(rule.Directions, direction) && phasesContain(rule.Phases, kind) {
				noiseByWire[rule.Wire] = rule
			}
		}
	}
	extraFields, extraBytes := 0, 0
	for name, value := range raw {
		if allowed[name] {
			continue
		}
		rule, exists := noiseByWire[name]
		if !exists || validateGenerated(value, rule.Generator) != nil {
			return protocol.Envelope{}, nil, nil, fmt.Errorf("undeclared or invalid obfs field %s", name)
		}
		extraFields++
		extraBytes += len(name) + len(value) + 4
	}
	if extraFields > p.Descriptor.Limits.MaxExtraFieldsPerMessage ||
		extraBytes > p.Descriptor.Limits.MaxExtraBytesPerMessage {
		return protocol.Envelope{}, nil, nil, errors.New("obfs noise field budget exceeded")
	}
	canonical, err := json.Marshal(logical)
	if err != nil {
		return protocol.Envelope{}, nil, nil, err
	}
	var env protocol.Envelope
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&env); err != nil {
		return protocol.Envelope{}, nil, nil, err
	}
	if env.V != protocol.Version {
		return protocol.Envelope{}, nil, nil, errors.New("unsupported protocol version")
	}
	return env, nil, nil, nil
}

func validateDecoy(raw map[string]json.RawMessage, rule *DecoyRule) error {
	allowed := map[string]bool{}
	for _, field := range rule.Fields {
		allowed[field.Wire] = true
		value, exists := raw[field.Wire]
		if !exists {
			return fmt.Errorf("obfs decoy field %s is missing", field.Wire)
		}
		switch {
		case field.Generator != nil:
			if err := validateGenerated(value, *field.Generator); err != nil {
				return err
			}
		case field.CopyFromRequest != "":
			if len(value) > 4096 {
				return errors.New("obfs copied decoy field is too large")
			}
		default:
			expected, _ := json.Marshal(field.Value)
			if !jsonEqual(value, expected) {
				return errors.New("obfs decoy constant mismatch")
			}
		}
	}
	for name := range raw {
		if !allowed[name] {
			return fmt.Errorf("undeclared obfs decoy field %s", name)
		}
	}
	return nil
}

func readWireLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > protocol.MaxLine {
			return nil, errors.New("obfs wire line too large")
		}
		line = append(line, part...)
		if err == nil {
			return bytes.TrimSuffix(line, []byte{'\n'}), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

func parseRawObject(line []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("obfs message must be a JSON object")
	}
	result := map[string]json.RawMessage{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("invalid obfs JSON key")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate obfs JSON key %q", key)
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	if _, err = dec.Token(); err != nil {
		return nil, err
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing obfs JSON data")
	}
	return result, nil
}

func encodeWireObject(fields []wireField) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	seen := map[string]bool{}
	for i, field := range fields {
		if field.name == "" || seen[field.name] {
			return nil, errors.New("duplicate or empty obfs output field")
		}
		seen[field.name] = true
		if i != 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(field.name)
		b.Write(name)
		b.WriteByte(':')
		b.Write(field.raw)
	}
	b.WriteString("}\n")
	if b.Len() > protocol.MaxLine {
		return nil, errors.New("obfs output line too large")
	}
	return b.Bytes(), nil
}

func generate(g Generator) (any, error) {
	switch g.Type {
	case "choice":
		return g.Values[randomInt(0, len(g.Values)-1)], nil
	case "bool":
		return chance(g.TrueProbability), nil
	case "int":
		return int64(math.Round(float64(randomInt64(int64(g.Min), int64(g.Max))))), nil
	case "uint":
		return uint64(randomInt64(int64(g.Min), int64(g.Max))), nil
	case "float":
		value := g.Min + randomFloat()*(g.Max-g.Min)
		scale := math.Pow10(g.Decimals)
		return math.Round(value*scale) / scale, nil
	case "hex":
		b := randomBytes(g.Bytes)
		return hex.EncodeToString(b), nil
	case "base64url":
		n := randomInt(g.MinBytes, g.MaxBytes)
		return base64.RawURLEncoding.EncodeToString(randomBytes(n)), nil
	case "uuid":
		b := randomBytes(16)
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
	case "unix_sec":
		return time.Now().Unix() + randomInt64(int64(-g.JitterSec), int64(g.JitterSec)), nil
	case "unix_ms":
		return time.Now().UnixMilli() + randomInt64(int64(-g.JitterMS), int64(g.JitterMS)), nil
	case "ascii":
		n := randomInt(g.MinLength, g.MaxLength)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(g.Alphabet[randomInt(0, len(g.Alphabet)-1)])
		}
		return b.String(), nil
	default:
		return nil, fmt.Errorf("unsupported generator %q", g.Type)
	}
}

func validateGenerated(raw json.RawMessage, g Generator) error {
	value, err := generateComparable(raw)
	if err != nil {
		return err
	}
	switch g.Type {
	case "choice":
		for _, candidate := range g.Values {
			want, _ := json.Marshal(candidate)
			if jsonEqual(raw, want) {
				return nil
			}
		}
		return errors.New("obfs choice value is outside allowed set")
	case "bool":
		if _, ok := value.(bool); !ok {
			return errors.New("obfs value is not boolean")
		}
	case "int", "uint", "float":
		number, ok := value.(json.Number)
		if !ok {
			return errors.New("obfs value is not numeric")
		}
		f, err := strconv.ParseFloat(string(number), 64)
		if err != nil || f < g.Min || f > g.Max {
			return errors.New("obfs numeric value is out of range")
		}
		if (g.Type == "int" || g.Type == "uint") && math.Trunc(f) != f {
			return errors.New("obfs integer value is fractional")
		}
		if g.Type == "uint" && f < 0 {
			return errors.New("obfs unsigned value is negative")
		}
	case "hex":
		s, ok := value.(string)
		decoded, err := hex.DecodeString(s)
		if !ok || err != nil || len(decoded) != g.Bytes {
			return errors.New("invalid obfs hex value")
		}
	case "base64url":
		s, ok := value.(string)
		decoded, err := base64.RawURLEncoding.DecodeString(s)
		if !ok || err != nil || len(decoded) < g.MinBytes || len(decoded) > g.MaxBytes {
			return errors.New("invalid obfs base64url value")
		}
	case "uuid", "ascii":
		s, ok := value.(string)
		if !ok || len(s) > 1024 {
			return errors.New("invalid obfs string value")
		}
	case "unix_sec", "unix_ms":
		if _, ok := value.(json.Number); !ok {
			return errors.New("invalid obfs timestamp value")
		}
	}
	return nil
}

func generateComparable(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	err := dec.Decode(&value)
	return value, err
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	dx := json.NewDecoder(bytes.NewReader(a))
	dx.UseNumber()
	dy := json.NewDecoder(bytes.NewReader(b))
	dy.UseNumber()
	return dx.Decode(&x) == nil && dy.Decode(&y) == nil && fmt.Sprint(x) == fmt.Sprint(y)
}

func chance(probability float64) bool {
	if probability <= 0 {
		return false
	}
	if probability >= 1 {
		return true
	}
	return randomFloat() < probability
}

func randomFloat() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := uint64(0)
	for _, value := range b {
		n = n<<8 | uint64(value)
	}
	return float64(n>>11) / (1 << 53)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randomInt(minimum, maximum int) int {
	return int(randomInt64(int64(minimum), int64(maximum)))
}

func randomInt64(minimum, maximum int64) int64 {
	if maximum <= minimum {
		return minimum
	}
	return minimum + int64(randomFloat()*float64(maximum-minimum+1))
}

func randomDuration(minimumMS, maximumMS int) time.Duration {
	if maximumMS < minimumMS {
		maximumMS = minimumMS
	}
	return time.Duration(randomInt(minimumMS, maximumMS)) * time.Millisecond
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}
