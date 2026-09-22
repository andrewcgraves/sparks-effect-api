package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromRequest_countZeroIgnoresXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	got := FromRequest(req, 0)
	if got != "192.0.2.1" {
		t.Errorf("count=0: got %q, want RemoteAddr host 192.0.2.1 (spoofed XFF ignored)", got)
	}
}

func TestFromRequest_countOneSkipsRightmostTrustedHop(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.50")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("count=1 with spoofed, real: got %q, want 203.0.113.50", got)
	}
}

func TestFromRequest_countOneSingleXFFEntryIsTheClient(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	// chain = [9.9.9.9, 10.0.0.1]; index = len-1-count = 0, so the single
	// XFF entry wins. That is a computed 0, not a clamp onto chain[0].
	// A replacing proxy (Railway writing only the client IP) and a client
	// spoofing XFF without a proxy that appends look identical at count=1;
	// spoof-resistance tests must send "spoofed, real" with RemoteAddr set
	// to the proxy.
	got := FromRequest(req, 1)
	if got != "9.9.9.9" {
		t.Errorf("count=1 with a single XFF entry: got %q, want 9.9.9.9", got)
	}
}

func TestFromRequest_joinsDuplicateXFFHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Add("X-Forwarded-For", "9.9.9.9")
	req.Header.Add("X-Forwarded-For", "203.0.113.50")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("duplicate XFF lines: got %q, want 203.0.113.50", got)
	}
}

func TestFromRequest_tooHighCountFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	got := FromRequest(req, 2)
	if got != "10.0.0.1" {
		t.Errorf("count=2 with a single XFF entry: got %q, want RemoteAddr 10.0.0.1 (not leftmost 9.9.9.9)", got)
	}
}

func TestFromRequest_canonicalizesIPv4MappedRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::ffff:192.0.2.1]:1"

	got := FromRequest(req, 0)
	if got != "192.0.2.1" {
		t.Errorf("IPv4-mapped RemoteAddr: got %q, want 192.0.2.1", got)
	}
}

func TestFromRequest_ipv6RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8::1]:1234"

	got := FromRequest(req, 0)
	if got != "2001:db8::1" {
		t.Errorf("IPv6 RemoteAddr: got %q, want 2001:db8::1", got)
	}
}

func TestFromRequest_emptyXFFUsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"

	got := FromRequest(req, 1)
	if got != "192.0.2.1" {
		t.Errorf("empty XFF: got %q, want 192.0.2.1", got)
	}
}

func TestFromRequest_whitespaceAroundCommas(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "9.9.9.9,  203.0.113.50")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("whitespace around commas: got %q, want 203.0.113.50", got)
	}
}

func TestFromRequest_skipsUnparseableTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	// Without the skip, count=1 would land on "not-an-ip".
	req.Header.Set("X-Forwarded-For", "203.0.113.50, not-an-ip")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("unparseable token: got %q, want 203.0.113.50", got)
	}
}

func TestFromRequest_httptestDefaultRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if req.RemoteAddr != "192.0.2.1:1234" {
		t.Fatalf("httptest RemoteAddr = %q, want 192.0.2.1:1234 (test assumption)", req.RemoteAddr)
	}
	got := FromRequest(req, 0)
	if got != "192.0.2.1" {
		t.Errorf("httptest default: got %q, want 192.0.2.1", got)
	}
}

func TestFromRequest_emptyRemoteAddrFallsBackToUnknown(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ""

	got := FromRequest(req, 0)
	if got != "unknown" {
		t.Errorf("empty RemoteAddr: got %q, want unknown", got)
	}
}

func TestFromRequest_countZeroIgnoresXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Real-IP", "9.9.9.9")

	got := FromRequest(req, 0)
	if got != "192.0.2.1" {
		t.Errorf("count=0: got %q, want RemoteAddr host 192.0.2.1 (X-Real-IP ignored)", got)
	}
}

func TestFromRequest_countOneEmptyXFFUsesXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Real-IP", "203.0.113.50")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("count=1 empty XFF: got %q, want X-Real-IP 203.0.113.50", got)
	}
}

func TestFromRequest_xffWalkIgnoresXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:443"
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.50")
	req.Header.Set("X-Real-IP", "198.51.100.1")

	got := FromRequest(req, 1)
	if got != "203.0.113.50" {
		t.Errorf("XFF present: got %q, want 203.0.113.50 from the XFF walk (not X-Real-IP)", got)
	}
}
