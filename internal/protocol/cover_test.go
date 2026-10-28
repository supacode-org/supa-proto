package protocol

import "testing"

func TestCoverOpenPayloadRoundTrip(t *testing.T) {
	for _, rate := range []uint16{1, 128, 600, 8000, 65535} {
		payload := CoverOpenPayload(rate)
		frame, err := DecodeFrame(mustEncode(t, Frame{Type: FrameCoverOpen, Payload: payload}))
		if err != nil {
			t.Fatalf("rate %d: decode: %v", rate, err)
		}
		got, err := ParseCoverOpen(frame.Payload)
		if err != nil || got != rate {
			t.Fatalf("rate %d: ParseCoverOpen = %d, %v", rate, got, err)
		}
	}
}

func TestDecodeFrameRejectsBadCoverOpen(t *testing.T) {
	cases := map[string][]byte{
		"too short":   {1, 0},
		"too long":    {1, 0, 0, 0},
		"bad version": {2, 0, 0},
	}
	for name, payload := range cases {
		if _, err := DecodeFrame(mustEncode(t, Frame{Type: FrameCoverOpen, Payload: payload})); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestCoverOpenResultFrame(t *testing.T) {
	frame, err := DecodeFrame(mustEncode(t, Frame{Type: FrameCoverOpenResult, Payload: Result(0, "")}))
	if err != nil || frame.Type != FrameCoverOpenResult {
		t.Fatalf("valid result rejected: %v", err)
	}
	if _, err := DecodeFrame(mustEncode(t, Frame{Type: FrameCoverOpenResult, Payload: nil})); err == nil {
		t.Fatal("empty COVER_OPEN_RESULT accepted")
	}
}

func mustEncode(t *testing.T, frame Frame) []byte {
	t.Helper()
	b, err := EncodeFrame(frame)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}
