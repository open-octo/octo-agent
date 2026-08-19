package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// jsonBool reads one boolean field out of a JSON response body.
func jsonBool(t *testing.T, body []byte, field string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	b, ok := m[field].(bool)
	if !ok {
		t.Fatalf("field %q is not a bool in %s", field, body)
	}
	return b
}

// localReq builds a request with an explicit peer address, Host, and headers —
// the three things isLocalRequest weighs.
func localReq(target, remoteAddr string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	return req
}

// A loopback peer address alone does not make a request local. Everything that
// forwards a port runs on this machine and dials us from loopback, so a remote
// client arrives looking local unless something else gives the relay away.
func TestIsLocalRequest(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		remoteAddr string
		hdr        map[string]string
		want       bool
	}{
		{"direct localhost", "http://localhost:8088/api/version", "127.0.0.1:50000", nil, true},
		{"direct 127.0.0.1", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000", nil, true},
		{"direct IPv6 loopback", "http://[::1]:8088/api/version", "[::1]:50000", nil, true},
		{"direct IPv4-mapped peer", "http://127.0.0.1:8088/api/version", "[::ffff:127.0.0.1]:50000", nil, true},

		// A browser on another machine: settled before this change, pinned here
		// so the added signals can't accidentally start admitting it.
		{"remote peer", "http://192.168.1.5:8088/api/version", "192.168.1.9:50000", nil, false},

		// ngrok's default shape: its agent runs here and dials loopback, but it
		// passes the public hostname through.
		{"tunnel keeps public host", "http://x.ngrok-free.app/api/version", "127.0.0.1:50000", nil, false},

		// …and with Host rewritten to loopback, the forwarding headers are what
		// remains to give it away.
		{"tunnel rewrites host, XFF remains", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{"X-Forwarded-For": "203.0.113.7"}, false},
		{"X-Forwarded-Host", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{"X-Forwarded-Host": "x.example"}, false},
		{"X-Forwarded-Proto", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{"X-Forwarded-Proto": "https"}, false},
		{"X-Real-Ip", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{"X-Real-Ip": "203.0.113.7"}, false},
		{"Forwarded", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{"Forwarded": "for=203.0.113.7"}, false},

		// octo's own managed tunnel, which drops the phone's Host and dials
		// loopback — the marker its bridge stamps is the only difference.
		{"octo tunnel marker", "http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
			map[string]string{HeaderForwarded: "relay"}, false},

		// DNS rebinding: an attacker domain resolving to 127.0.0.1 is not local.
		{"foreign host", "http://attacker.example:8088/api/version", "127.0.0.1:50000", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalRequest(localReq(tc.target, tc.remoteAddr, tc.hdr)); got != tc.want {
				t.Errorf("isLocalRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// Spoofing a forwarding header can only cost the sender the local-peer
// capabilities, never grant them — the asymmetry that makes reading a
// client-controlled header safe here at all.
func TestIsLocalRequest_SpoofedHeaderOnlyNarrows(t *testing.T) {
	// A genuinely local caller who forges the header just loses local status.
	if isLocalRequest(localReq("http://127.0.0.1:8088/api/version", "127.0.0.1:50000",
		map[string]string{"X-Forwarded-For": "127.0.0.1"})) {
		t.Error("a forged forwarding header must not be ignored")
	}
	// A remote caller stripping every header still isn't local: the peer
	// address is not theirs to choose.
	if isLocalRequest(localReq("http://localhost:8088/api/version", "203.0.113.9:50000", nil)) {
		t.Error("a remote peer claiming a loopback Host must not become local")
	}
}

// /api/version is unauthenticated and its `local` flag is what the frontend
// keys real-path file/folder selection off, so it must not report a relayed
// peer as local.
func TestHandleVersion_LocalFlag(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})

	check := func(name string, hdr map[string]string, want bool) {
		t.Helper()
		w := authRequest(t, srv, http.MethodGet, "http://127.0.0.1:8088/api/version", "127.0.0.1:50000", hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		if got := jsonBool(t, w.Body.Bytes(), "local"); got != want {
			t.Errorf("%s: local = %v, want %v", name, got, want)
		}
	}

	check("direct", nil, true)
	check("behind a proxy", map[string]string{"X-Forwarded-For": "203.0.113.7"}, false)
	check("octo tunnel", map[string]string{HeaderForwarded: "relay"}, false)
}

// The unauthenticated loopback exemption is the highest-stakes consumer: a
// forwarded port used to hand it to whoever found the URL, and a configured
// access key does not close it — this branch is the fallback for requests that
// carry no key at all.
func TestRequireAuth_ForwardedRequestLosesExemption(t *testing.T) {
	srv := mustServer(t, Config{AccessKey: testAccessKey})

	// Baseline: the exemption still works for a genuine local caller.
	if w := authRequest(t, srv, http.MethodGet, "http://127.0.0.1:8088/api/sessions", "127.0.0.1:50000", nil); w.Code != http.StatusOK {
		t.Fatalf("direct loopback: got %d, want 200", w.Code)
	}

	for _, h := range forwardedHeaders {
		t.Run(h, func(t *testing.T) {
			w := authRequest(t, srv, http.MethodGet, "http://127.0.0.1:8088/api/sessions", "127.0.0.1:50000",
				map[string]string{h: "1"})
			if w.Code != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", w.Code)
			}
		})
	}

	// A forwarded request that DOES carry the key is fine — the key is the
	// gate for remote peers, and narrowing the exemption must not break it.
	w := authRequest(t, srv, http.MethodGet, "http://127.0.0.1:8088/api/sessions", "127.0.0.1:50000",
		map[string]string{"X-Forwarded-For": "203.0.113.7", "X-Access-Key": testAccessKey})
	if w.Code != http.StatusOK {
		t.Errorf("forwarded request with a valid key: got %d, want 200", w.Code)
	}
}
