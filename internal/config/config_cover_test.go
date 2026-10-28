package config

import (
	"bytes"
	"encoding/base64"
	"testing"
)

var testSecret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))

func validCoverClient() Client {
	c := Client{Server: "example:8443", ClientID: "u", Secret: testSecret}
	c.Defaults()
	return c
}

func validCoverServer() Server {
	s := Server{Users: []User{{ClientID: "u", Secret: testSecret, Status: "enabled"}}}
	s.Defaults()
	return s
}

func TestCoverClientValidation(t *testing.T) {
	c := validCoverClient()
	c.Cover = CoverClient{Enabled: true, RateKbps: 1000}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid cover rejected: %v", err)
	}
	c.Cover.RateKbps = 10
	if err := c.Validate(); err == nil {
		t.Fatal("rate below floor accepted")
	}
	c.Cover.RateKbps = 200000
	if err := c.Validate(); err == nil {
		t.Fatal("rate above ceiling accepted")
	}
	// Disabled cover ignores the rate entirely.
	c.Cover = CoverClient{Enabled: false, RateKbps: 0}
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled cover rejected: %v", err)
	}
}

func TestCoverClientDefaultRate(t *testing.T) {
	c := Client{Server: "example:8443", ClientID: "u", Secret: testSecret, Cover: CoverClient{Enabled: true}}
	c.Defaults()
	if c.Cover.RateKbps != 1000 {
		t.Fatalf("default rate = %d, want 1000", c.Cover.RateKbps)
	}
}

func TestCoverServerValidationAndDefaults(t *testing.T) {
	s := Server{
		Cover: CoverServer{Enabled: true},
		Users: []User{{ClientID: "u", Secret: testSecret, Status: "enabled"}},
	}
	s.Defaults()
	if s.Cover.MaxRateKbpsPerUser == 0 || s.Cover.MaxSessionsPerUser == 0 {
		t.Fatalf("server cover defaults not applied: %+v", s.Cover)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("valid server cover rejected: %v", err)
	}
	s.Cover.MaxSessionsPerUser = 100
	if err := s.Validate(); err == nil {
		t.Fatal("session cap above hard limit accepted")
	}
}
