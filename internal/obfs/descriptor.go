package obfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

type Config struct {
	Provider      string  `json:"provider"`
	Participation float64 `json:"participation"`
}

type Limits struct {
	MaxExtraFieldsPerMessage int     `json:"maxExtraFieldsPerMessage"`
	MaxExtraBytesPerMessage  int     `json:"maxExtraBytesPerMessage"`
	MaxDecoyBytes            int     `json:"maxDecoyBytes"`
	MaxDecoysPerSecond       int     `json:"maxDecoysPerSecond"`
	MaxDecoysPerMinute       int     `json:"maxDecoysPerMinute"`
	MaxOverheadRatio         float64 `json:"maxOverheadRatio"`
	MaxConsecutiveDecoys     int     `json:"maxConsecutiveDecoys"`
	MaxPreAuthDecoys         int     `json:"maxPreAuthDecoys"`
}

type Descriptor struct {
	APIVersion    int                        `json:"apiVersion"`
	ID            string                     `json:"id"`
	Version       string                     `json:"version"`
	Description   string                     `json:"description"`
	Limits        Limits                     `json:"limits"`
	Discriminator Discriminator              `json:"discriminator"`
	Messages      map[string]MessageTemplate `json:"messages"`
	NoiseFields   map[string]NoiseRule       `json:"noiseFields"`
	Decoys        []DecoyRule                `json:"decoys"`
	// Cover is optional. When present, the provider supports cross-connection
	// cover traffic (see the cover-traffic spec): extra sessions that carry no
	// real target and generate steady, video-like decoy data in Direction.
	Cover *CoverProfile `json:"cover"`
}

// CoverProfile describes how a provider shapes cover traffic. Direction is the
// side that generates data ("s2c" streams down like a camera feed, "c2s"
// uploads like a bulk database load). The chunk and interval ranges bound the
// per-frame payload size and spacing; the shaper samples within them.
type CoverProfile struct {
	Direction     string `json:"direction"`
	ChunkBytesMin int    `json:"chunkBytesMin"`
	ChunkBytesMax int    `json:"chunkBytesMax"`
	IntervalMsMin int    `json:"intervalMsMin"`
	IntervalMsMax int    `json:"intervalMsMax"`
}

type Discriminator struct {
	Field         string            `json:"field"`
	LogicalValues map[string]string `json:"logicalValues"`
}

type MessageTemplate struct {
	Constants  []Constant  `json:"constants"`
	Fields     []FieldMap  `json:"fields"`
	NoiseSlots []NoiseSlot `json:"noiseSlots"`
}

type Constant struct {
	Wire  string `json:"wire"`
	Value any    `json:"value"`
}

type FieldMap struct {
	Logical string `json:"logical"`
	Wire    string `json:"wire"`
	Codec   string `json:"codec"`
}

type NoiseSlot struct {
	After      string   `json:"after"`
	Candidates []string `json:"candidates"`
}

type NoiseRule struct {
	Wire            string    `json:"wire"`
	Phases          []string  `json:"phases"`
	Directions      []string  `json:"directions"`
	BaseProbability float64   `json:"baseProbability"`
	Generator       Generator `json:"generator"`
}

type Generator struct {
	Type            string  `json:"type"`
	Values          []any   `json:"values"`
	Min             float64 `json:"min"`
	Max             float64 `json:"max"`
	Decimals        int     `json:"decimals"`
	Bytes           int     `json:"bytes"`
	MinBytes        int     `json:"minBytes"`
	MaxBytes        int     `json:"maxBytes"`
	JitterSec       int     `json:"jitterSec"`
	JitterMS        int     `json:"jitterMs"`
	Alphabet        string  `json:"alphabet"`
	MinLength       int     `json:"minLength"`
	MaxLength       int     `json:"maxLength"`
	TrueProbability float64 `json:"trueProbability"`
}

type Trigger struct {
	Type              string `json:"type"`
	MinMS             int    `json:"minMs"`
	MaxMS             int    `json:"maxMs"`
	AfterRealMessages int    `json:"afterRealMessages"`
}

type ResponseRule struct {
	Decoy       string  `json:"decoy"`
	Probability float64 `json:"probability"`
	MinDelayMS  int     `json:"minDelayMs"`
	MaxDelayMS  int     `json:"maxDelayMs"`
}

type DecoyField struct {
	Wire            string     `json:"wire"`
	Value           any        `json:"value"`
	Generator       *Generator `json:"generator"`
	CopyFromRequest string     `json:"copyFromRequest"`
}

type DecoyRule struct {
	ID              string        `json:"id"`
	WireEvent       string        `json:"wireEvent"`
	Directions      []string      `json:"directions"`
	Phases          []string      `json:"phases"`
	Trigger         Trigger       `json:"trigger"`
	BaseProbability float64       `json:"baseProbability"`
	Response        *ResponseRule `json:"response"`
	ResponseOnly    bool          `json:"responseOnly"`
	Fields          []DecoyField  `json:"fields"`
}

type Provider struct {
	Descriptor         Descriptor
	Participation      float64
	ParticipationMilli uint16
	SourceSHA256       [32]byte
	identityWire       bool
	identity           []byte
	wireToKind         map[string]string
	decoyByEvent       map[string]*DecoyRule
	decoyByID          map[string]*DecoyRule
}

func descriptorFromObject(object map[string]any, source []byte, participation float64) (*Provider, error) {
	data, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var descriptor Descriptor
	if err = dec.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf("decode obfs descriptor: %w", err)
	}
	p := &Provider{Descriptor: descriptor, Participation: participation, SourceSHA256: sha256.Sum256(source)}
	if descriptor.ID == "none" {
		p.Participation = 0
	}
	p.ParticipationMilli = uint16(p.Participation*1000 + 0.5)
	if err = p.validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	writeIdentity := func(value []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(value)))
		b.Write(n[:])
		b.Write(value)
	}
	writeIdentity([]byte("obfs-provider/v1"))
	writeIdentity([]byte(descriptor.ID))
	writeIdentity([]byte(descriptor.Version))
	writeIdentity(p.SourceSHA256[:])
	var part [2]byte
	binary.BigEndian.PutUint16(part[:], p.ParticipationMilli)
	writeIdentity(part[:])
	p.identity = b.Bytes()
	return p, nil
}

func (p *Provider) Identity() []byte         { return append([]byte(nil), p.identity...) }
func (p *Provider) ID() string               { return p.Descriptor.ID }
func (p *Provider) Hash() string             { return fmt.Sprintf("%x", p.SourceSHA256) }
func (p *Provider) IdentityWireFormat() bool { return p.identityWire }

func (p *Provider) validate() error {
	d := &p.Descriptor
	if d.APIVersion != 1 {
		return errors.New("obfs apiVersion must be 1")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`).MatchString(d.ID) {
		return errors.New("invalid obfs provider id")
	}
	if d.Version == "" || len(d.Version) > 32 {
		return errors.New("invalid obfs provider version")
	}
	if p.Participation < 0 || p.Participation > 1 {
		return errors.New("obfs participation must be between 0 and 1")
	}
	if d.Discriminator.Field == "" {
		return errors.New("obfs discriminator field is required")
	}
	required := map[string][]string{
		"client_hello": {"v", "kind", "client_id", "ts", "cn", "ce", "otp", "mac"},
		"server_hello": {"v", "kind", "sid", "sn", "se", "exp", "mac"},
		"frame":        {"v", "kind", "sid", "dir", "seq", "data"},
		"error":        {"v", "kind", "code", "message"},
	}
	p.wireToKind = map[string]string{}
	p.decoyByEvent = map[string]*DecoyRule{}
	p.decoyByID = map[string]*DecoyRule{}
	validCodecs := map[string]bool{
		"uint": true, "uint64": true, "int64": true, "string": true,
		"base64url": true, "decimal": true, "enum": true, "discriminator": true,
	}
	for kind, fields := range required {
		template, ok := d.Messages[kind]
		if !ok {
			return fmt.Errorf("obfs message template %q is required", kind)
		}
		value := d.Discriminator.LogicalValues[kind]
		if value == "" {
			return fmt.Errorf("obfs discriminator value for %q is required", kind)
		}
		if _, exists := p.wireToKind[value]; exists {
			return errors.New("duplicate obfs discriminator value")
		}
		p.wireToKind[value] = kind
		seenLogical := map[string]bool{}
		seenWire := map[string]bool{}
		for _, constant := range template.Constants {
			if constant.Wire == "" || seenWire[constant.Wire] {
				return fmt.Errorf("invalid or duplicate constant in %s", kind)
			}
			seenWire[constant.Wire] = true
		}
		for _, field := range template.Fields {
			if field.Logical == "" || field.Wire == "" || !validCodecs[field.Codec] ||
				seenLogical[field.Logical] || seenWire[field.Wire] {
				return fmt.Errorf("invalid or duplicate field mapping in %s", kind)
			}
			seenLogical[field.Logical], seenWire[field.Wire] = true, true
		}
		if !seenWire[d.Discriminator.Field] {
			return fmt.Errorf("message %s does not map discriminator field %s", kind, d.Discriminator.Field)
		}
		for _, field := range fields {
			if !seenLogical[field] {
				return fmt.Errorf("obfs template %s is missing logical field %s", kind, field)
			}
		}
	}
	noiseWire := map[string]bool{}
	for id, rule := range d.NoiseFields {
		if id == "" || rule.Wire == "" || rule.BaseProbability < 0 || rule.BaseProbability > 1 {
			return fmt.Errorf("invalid obfs noise rule %q", id)
		}
		if noiseWire[rule.Wire] {
			return fmt.Errorf("duplicate noise wire field %q", rule.Wire)
		}
		if err := validateDirections(rule.Directions); err != nil {
			return fmt.Errorf("noise rule %s: %w", id, err)
		}
		if err := validatePhases(rule.Phases, false); err != nil {
			return fmt.Errorf("noise rule %s: %w", id, err)
		}
		noiseWire[rule.Wire] = true
		if err := validateGenerator(rule.Generator); err != nil {
			return fmt.Errorf("noise rule %s: %w", id, err)
		}
	}
	for kind, template := range d.Messages {
		for _, slot := range template.NoiseSlots {
			for _, candidate := range slot.Candidates {
				if _, ok := d.NoiseFields[candidate]; !ok {
					return fmt.Errorf("message %s references unknown noise rule %s", kind, candidate)
				}
			}
		}
	}
	for i := range d.Decoys {
		rule := &d.Decoys[i]
		if rule.ID == "" || rule.WireEvent == "" || p.decoyByEvent[rule.WireEvent] != nil || p.decoyByID[rule.ID] != nil {
			return errors.New("invalid or duplicate obfs decoy")
		}
		if rule.BaseProbability < 0 || rule.BaseProbability > 1 {
			return fmt.Errorf("invalid probability for decoy %s", rule.ID)
		}
		if err := validateDirections(rule.Directions); err != nil {
			return fmt.Errorf("decoy %s: %w", rule.ID, err)
		}
		if err := validatePhases(rule.Phases, true); err != nil {
			return fmt.Errorf("decoy %s: %w", rule.ID, err)
		}
		if _, collision := p.wireToKind[rule.WireEvent]; collision {
			return errors.New("decoy discriminator collides with logical message")
		}
		p.decoyByEvent[rule.WireEvent], p.decoyByID[rule.ID] = rule, rule
		for _, field := range rule.Fields {
			if field.Wire == "" {
				return errors.New("decoy field wire name is required")
			}
			if field.Generator != nil {
				if err := validateGenerator(*field.Generator); err != nil {
					return err
				}
			}
		}
	}
	for _, rule := range d.Decoys {
		if rule.Response != nil {
			response := p.decoyByID[rule.Response.Decoy]
			if response == nil || !response.ResponseOnly || response.Response != nil {
				return fmt.Errorf("invalid decoy response target %q", rule.Response.Decoy)
			}
			if rule.Response.Probability < 0 || rule.Response.Probability > 1 ||
				rule.Response.MinDelayMS < 0 || rule.Response.MaxDelayMS < rule.Response.MinDelayMS ||
				rule.Response.MaxDelayMS > 60000 {
				return fmt.Errorf("invalid response timing for decoy %s", rule.ID)
			}
		}
	}
	if err := p.validateCover(); err != nil {
		return err
	}
	p.applyLimitDefaults()
	return nil
}

// coverChunkCeiling bounds a single cover payload so the encoded wire line
// (AEAD tag, base64 and JSON field names) stays well under protocol.MaxLine.
const coverChunkCeiling = 48 << 10

func (p *Provider) validateCover() error {
	c := p.Descriptor.Cover
	if c == nil {
		return nil
	}
	if c.Direction != "c2s" && c.Direction != "s2c" {
		return errors.New("obfs cover direction must be c2s or s2c")
	}
	if c.ChunkBytesMin == 0 {
		c.ChunkBytesMin = 4096
	}
	if c.ChunkBytesMax == 0 {
		c.ChunkBytesMax = coverChunkCeiling
	}
	if c.IntervalMsMin == 0 {
		c.IntervalMsMin = 40
	}
	if c.IntervalMsMax == 0 {
		c.IntervalMsMax = 400
	}
	if c.ChunkBytesMin < 256 || c.ChunkBytesMax > coverChunkCeiling || c.ChunkBytesMin > c.ChunkBytesMax {
		return errors.New("obfs cover chunk range must be within [256, 49152] with min <= max")
	}
	if c.IntervalMsMin < 1 || c.IntervalMsMax > 5000 || c.IntervalMsMin > c.IntervalMsMax {
		return errors.New("obfs cover interval range must be within [1, 5000]ms with min <= max")
	}
	return nil
}

// Cover returns the provider's cover profile, or nil when it does not support
// cover traffic.
func (p *Provider) Cover() *CoverProfile { return p.Descriptor.Cover }

func validateDirections(values []string) error {
	if len(values) == 0 || len(values) > 2 {
		return errors.New("directions must contain c2s and/or s2c")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if (value != "c2s" && value != "s2c") || seen[value] {
			return errors.New("invalid or duplicate direction")
		}
		seen[value] = true
	}
	return nil
}

func validatePhases(values []string, decoy bool) error {
	if len(values) == 0 || len(values) > 4 {
		return errors.New("phases must be a non-empty bounded list")
	}
	valid := map[string]bool{
		"client_hello": true, "server_hello": true, "frame": true, "error": true,
	}
	if decoy {
		valid = map[string]bool{
			"before_hello": true, "handshake": true, "established": true, "idle": true,
		}
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !valid[value] || seen[value] {
			return errors.New("invalid or duplicate phase")
		}
		seen[value] = true
	}
	return nil
}

func (p *Provider) applyLimitDefaults() {
	l := &p.Descriptor.Limits
	if l.MaxExtraFieldsPerMessage <= 0 || l.MaxExtraFieldsPerMessage > 12 {
		l.MaxExtraFieldsPerMessage = 8
	}
	if l.MaxExtraBytesPerMessage <= 0 || l.MaxExtraBytesPerMessage > 2048 {
		l.MaxExtraBytesPerMessage = 2048
	}
	if l.MaxDecoyBytes <= 0 || l.MaxDecoyBytes > 4096 {
		l.MaxDecoyBytes = 2048
	}
	if l.MaxDecoysPerSecond <= 0 || l.MaxDecoysPerSecond > 4 {
		l.MaxDecoysPerSecond = 2
	}
	if l.MaxDecoysPerMinute <= 0 || l.MaxDecoysPerMinute > 120 {
		l.MaxDecoysPerMinute = 60
	}
	if l.MaxOverheadRatio <= 0 || l.MaxOverheadRatio > .20 {
		l.MaxOverheadRatio = .20
	}
	if l.MaxConsecutiveDecoys <= 0 || l.MaxConsecutiveDecoys > 4 {
		l.MaxConsecutiveDecoys = 4
	}
	if l.MaxPreAuthDecoys <= 0 || l.MaxPreAuthDecoys > 2 {
		l.MaxPreAuthDecoys = 2
	}
}

func validateGenerator(g Generator) error {
	switch g.Type {
	case "choice":
		if len(g.Values) == 0 || len(g.Values) > 64 {
			return errors.New("choice generator requires 1-64 values")
		}
	case "bool":
		if g.TrueProbability < 0 || g.TrueProbability > 1 {
			return errors.New("invalid bool probability")
		}
	case "int", "uint", "float":
		if g.Max < g.Min || g.Decimals < 0 || g.Decimals > 6 {
			return errors.New("invalid numeric generator range")
		}
	case "hex":
		if g.Bytes < 1 || g.Bytes > 64 {
			return errors.New("invalid hex byte count")
		}
	case "base64url":
		if g.MinBytes < 1 || g.MaxBytes < g.MinBytes || g.MaxBytes > 256 {
			return errors.New("invalid base64url range")
		}
	case "uuid":
	case "unix_sec":
		if g.JitterSec < 0 || g.JitterSec > 86400 {
			return errors.New("invalid unix_sec jitter")
		}
	case "unix_ms":
		if g.JitterMS < 0 || g.JitterMS > 86400000 {
			return errors.New("invalid unix_ms jitter")
		}
	case "ascii":
		if g.Alphabet == "" || g.MinLength < 0 || g.MaxLength < g.MinLength || g.MaxLength > 1024 {
			return errors.New("invalid ascii generator")
		}
	default:
		return fmt.Errorf("unsupported generator type %q", g.Type)
	}
	return nil
}

func directionsContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func phasesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
