package ratelimit

import (
	"net"
	"net/http"
	"strings"
)

func ClientIP(trustedProxyCount int) func(*http.Request) string {
	if trustedProxyCount < 0 {
		trustedProxyCount = 0
	}
	return func(r *http.Request) string {
		return FromRequest(r, trustedProxyCount)
	}
}

func FromRequest(r *http.Request, trustedProxyCount int) string {
	if trustedProxyCount < 0 {
		trustedProxyCount = 0
	}
	remote := hostFromRemoteAddr(r.RemoteAddr)
	if trustedProxyCount == 0 {
		// Zero ignores forwarded headers: the only spoof-proof setting when
		// nothing trusted sits in front of us. Local .env.example uses 0;
		// production Load() defaults to 1 for Railway's reverse proxy.
		if remote != "" {
			return remote
		}
		return "unknown"
	}

	// The chain is every X-Forwarded-For value (RFC 7230 concatenates
	// duplicate header lines into one comma list; Header.Get would keep
	// only the first, which a client can spoof) followed by RemoteAddr,
	// the TCP peer that actually connected to us. Left is the original
	// client, right is the most recent hop.
	//
	// We skip N hops from the right. Railway's hop count has drifted: a
	// CDN/Fastly layer may add a hop, and X-Real-IP is documented but has
	// been the Fastly POP. Too high an N walks into spoofed leftmost
	// values — if buckets look shared across users, try 2, do not keep
	// raising. Leftmost-XFF is not the selector: a client-controlled
	// leftmost entry must not pick the bucket.
	//
	// When N is larger than the chain, fall back to RemoteAddr rather
	// than clamping onto chain[0]. A single XFF entry at count=1 is
	// still the client: chain=[xff, remote], idx=0, XFF replaces the
	// proxy. That is computed 0, not a clamp.
	chain := parseXFF(strings.Join(r.Header.Values("X-Forwarded-For"), ","))
	if len(chain) == 0 {
		// Railway's documented single-value header, overwritten at the
		// edge. Used only when XFF is empty so a Fastly-bugged X-Real-IP
		// cannot override a correct XFF walk.
		if ip := parseSingleIP(r.Header.Get("X-Real-IP")); ip != "" {
			chain = append(chain, ip)
		}
	}
	if remote != "" {
		chain = append(chain, remote)
	}
	if len(chain) == 0 {
		return "unknown"
	}
	idx := len(chain) - 1 - trustedProxyCount
	if idx < 0 {
		if remote != "" {
			return remote
		}
		return "unknown"
	}
	return chain[idx]
}

func parseXFF(header string) []string {
	if header == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(header, ",") {
		if ip := parseSingleIP(part); ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

func parseSingleIP(part string) string {
	part = strings.TrimSpace(part)
	if part == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(part); err == nil {
		part = host
	}
	part = strings.Trim(part, "[]")
	ip := net.ParseIP(part)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func hostFromRemoteAddr(remote string) string {
	if remote == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return parseSingleIP(host)
	}
	return parseSingleIP(strings.Trim(remote, "[]"))
}
