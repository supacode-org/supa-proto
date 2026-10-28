package obfs

import "time"

// coverWireOverhead approximates how many wire bytes one cover payload byte
// becomes after the AEAD tag, base64 and JSON field names are added. It lets
// the shaper pace to a wire-level rate without measuring each encoded line.
const coverWireOverhead = 1.4

// coverFrameConstant is the fixed plaintext added to every DATA frame before
// base64 (7-byte frame header plus the 16-byte AEAD tag).
const coverFrameConstant = 23

// Shaper produces a sequence of cover payload sizes and inter-frame delays
// that approximate a steady, video-like wire rate. The generating side calls
// Next before each frame. It is not safe for concurrent use; give each cover
// session its own shaper.
//
// Pacing is driven by the target rate: a payload size is sampled from the
// provider's chunk range and the delay is derived so that size/delay matches
// the per-session payload rate. The provider's interval range only clamps the
// cadence into a plausible band; it is kept wide enough not to distort the
// rate for 128-256 kbps connections.
type Shaper struct {
	profile            CoverProfile
	payloadBytesPerSec float64
	started            time.Time
	warmup             time.Duration
	warmupBoost        float64
}

// NewShaper returns a shaper for one cover session targeting rateKbps of wire
// throughput, with a short higher-rate "buffering" burst at the start.
func NewShaper(profile CoverProfile, rateKbps int) *Shaper {
	return &Shaper{
		profile:            profile,
		payloadBytesPerSec: float64(rateKbps) * 1000.0 / 8.0 / coverWireOverhead,
		started:            time.Now(),
		warmup:             time.Duration(3000+randomInt(0, 4000)) * time.Millisecond,
		warmupBoost:        1.5 + randomFloat(),
	}
}

// Next returns the next cover payload size in bytes and the delay to wait
// before sending it.
func (s *Shaper) Next() (size int, delay time.Duration) {
	rate := s.payloadBytesPerSec
	if time.Since(s.started) < s.warmup {
		rate *= s.warmupBoost
	}
	size = randomInt(s.profile.ChunkBytesMin, s.profile.ChunkBytesMax)
	seconds := float64(size) / rate
	minSec := float64(s.profile.IntervalMsMin) / 1000.0
	maxSec := float64(s.profile.IntervalMsMax) / 1000.0
	if seconds < minSec {
		seconds = minSec
	}
	if seconds > maxSec {
		seconds = maxSec
	}
	// Match the payload to the (possibly clamped) cadence so the rate holds.
	size = int(seconds * rate)
	if size < s.profile.ChunkBytesMin {
		size = s.profile.ChunkBytesMin
	}
	if size > s.profile.ChunkBytesMax {
		size = s.profile.ChunkBytesMax
	}
	jitter := 0.85 + 0.3*randomFloat()
	delay = time.Duration(seconds * jitter * float64(time.Second))
	return size, delay
}
