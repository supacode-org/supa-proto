package obfs

import (
	"testing"
	"time"
)

func TestBuiltInProvidersDeclareCoverDirection(t *testing.T) {
	cases := map[string]string{"iot-cam": "s2c", "mysql": "c2s"}
	for name, want := range cases {
		provider, err := Load(Config{Provider: name, Participation: 0.65})
		if err != nil {
			t.Fatal(err)
		}
		cover := provider.Cover()
		if cover == nil {
			t.Fatalf("%s: expected a cover profile", name)
		}
		if cover.Direction != want {
			t.Fatalf("%s: cover direction = %q, want %q", name, cover.Direction, want)
		}
	}
	none, err := Load(Config{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if none.Cover() != nil {
		t.Fatal("none provider must not declare cover")
	}
}

func TestValidateCover(t *testing.T) {
	bad := []CoverProfile{
		{Direction: "down"},
		{Direction: "s2c", ChunkBytesMin: 100},     // below floor
		{Direction: "s2c", ChunkBytesMax: 1 << 20}, // above ceiling
		{Direction: "s2c", ChunkBytesMin: 20000, ChunkBytesMax: 10000},
		{Direction: "s2c", IntervalMsMin: 0, IntervalMsMax: 0, ChunkBytesMin: 4096, ChunkBytesMax: 8192},
	}
	// The zero-interval case is only invalid when explicitly out of range; the
	// defaults path fills zeros. Test an explicitly reversed interval instead.
	bad[4] = CoverProfile{Direction: "s2c", IntervalMsMin: 500, IntervalMsMax: 100}
	for i, profile := range bad {
		p := &Provider{Descriptor: Descriptor{Cover: &profile}}
		if err := p.validateCover(); err == nil {
			t.Fatalf("case %d: expected rejection for %+v", i, profile)
		}
	}
	// A direction-only profile is filled with valid defaults.
	ok := &Provider{Descriptor: Descriptor{Cover: &CoverProfile{Direction: "c2s"}}}
	if err := ok.validateCover(); err != nil {
		t.Fatalf("direction-only profile rejected: %v", err)
	}
	filled := ok.Descriptor.Cover
	if filled.ChunkBytesMin == 0 || filled.ChunkBytesMax == 0 || filled.IntervalMsMin == 0 || filled.IntervalMsMax == 0 {
		t.Fatalf("defaults not applied: %+v", filled)
	}
}

func TestShaperStaysWithinBounds(t *testing.T) {
	profile := CoverProfile{Direction: "s2c", ChunkBytesMin: 8192, ChunkBytesMax: 49152, IntervalMsMin: 300, IntervalMsMax: 5000}
	s := NewShaper(profile, 200)
	// The ±15% delay jitter can nudge a delay just outside the raw band.
	lo := time.Duration(float64(profile.IntervalMsMin)*0.85) * time.Millisecond
	hi := time.Duration(float64(profile.IntervalMsMax)*1.15) * time.Millisecond
	for i := 0; i < 500; i++ {
		size, delay := s.Next()
		if size < profile.ChunkBytesMin || size > profile.ChunkBytesMax {
			t.Fatalf("size %d out of [%d,%d]", size, profile.ChunkBytesMin, profile.ChunkBytesMax)
		}
		if delay < lo || delay > hi {
			t.Fatalf("delay %s out of [%s,%s]", delay, lo, hi)
		}
	}
}
