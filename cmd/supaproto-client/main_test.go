package main

import (
	"bufio"
	"bytes"
	"net"
	"testing"
)

func TestSOCKSRequestUDPAllowsZeroPort(t *testing.T) {
	request := []byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}
	got, err := socksRequest(bufio.NewReader(bytes.NewReader(request)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != 3 || got.AddressType != 1 || got.Host != "0.0.0.0" || got.Port != 0 {
		t.Fatalf("unexpected UDP ASSOCIATE: %#v", got)
	}
}

func TestUDPEndpointPinsFirstPort(t *testing.T) {
	var endpoint udpEndpoint
	ip := net.ParseIP("127.0.0.1")
	if !endpoint.setOrMatch(&net.UDPAddr{IP: ip, Port: 12000}, ip, 0) {
		t.Fatal("first endpoint rejected")
	}
	if endpoint.setOrMatch(&net.UDPAddr{IP: ip, Port: 12001}, ip, 0) {
		t.Fatal("endpoint port was not pinned")
	}
	if !endpoint.setOrMatch(&net.UDPAddr{IP: ip, Port: 12000}, ip, 0) {
		t.Fatal("pinned endpoint rejected")
	}
}
