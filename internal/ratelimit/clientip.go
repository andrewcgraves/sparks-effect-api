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
	// The chain is X-Forwarded-For (left = original client, right = most
	// recent proxy) followed by RemoteAddr, which is the TCP peer that
	// actually connected to us. We do not read X-Real-IP: it is a single
	// spoofable value with no hop list to walk.
	//
	// TRUSTED_PROXY_COUNT is how many addresses to skip from the right.
	// Railway terminates TLS at one reverse proxy, so production Load()
	// defaults the count to 1: the client is one hop left of RemoteAddr.
	// Zero means ignore X-Forwarded-For entirely — the only spoof-proof
	// setting when nothing trusted sits in front of us.
	chain := parseXFF(r.Header.Get("X-Forwarded-For"))
	if remote != "" {
		chain = append(chain, remote)
	}
	if len(chain) == 0 {
		return "unknown"
	}
	idx := len(chain) - 1 - trustedProxyCount
	if idx < 0 {
		idx = 0
	}
	return chain[idx]
}

func parseXFF(header string) []string {
	if header == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(part); err == nil {
			part = host
		}
		part = strings.Trim(part, "[]")
		if net.ParseIP(part) == nil {
			continue
		}
		out = append(out, part)
	}
	return out
}

func hostFromRemoteAddr(remote string) string {
	if remote == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return strings.Trim(remote, "[]")
}
