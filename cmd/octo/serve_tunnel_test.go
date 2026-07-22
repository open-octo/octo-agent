package main

import (
	"encoding/hex"
	"testing"
)

func TestLoopbackWSURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8088":   "ws://127.0.0.1:8088/ws",
		":8088":            "ws://127.0.0.1:8088/ws",   // empty host = wildcard, dial loopback
		"0.0.0.0:9000":     "ws://127.0.0.1:9000/ws",   // IPv4 wildcard
		"[::]:8088":        "ws://127.0.0.1:8088/ws",   // IPv6 wildcard
		"192.168.1.5:8088": "ws://192.168.1.5:8088/ws", // specific bind dialed as-is
		"localhost:8088":   "ws://localhost:8088/ws",
	}
	for addr, want := range cases {
		if got := loopbackWSURL(addr); got != want {
			t.Errorf("loopbackWSURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestNewPairToken(t *testing.T) {
	a, err := newPairToken()
	if err != nil {
		t.Fatalf("newPairToken: %v", err)
	}
	if len(a) != 32 { // 16 bytes hex
		t.Errorf("token %q has length %d, want 32", a, len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("token %q is not hex: %v", a, err)
	}
	b, _ := newPairToken()
	if a == b {
		t.Error("two tokens should differ")
	}
}
