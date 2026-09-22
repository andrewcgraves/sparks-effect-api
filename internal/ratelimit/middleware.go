package ratelimit

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

const ErrorCode = "rate_limited"

const rateLimitedMessage = "Too many requests. Please try again shortly."

func Limit(n *Limiter, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	if n == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	if clientIP == nil {
		clientIP = ClientIP(0)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if ip == "" {
				ip = "unknown"
			}
			// User is the primary key when a session is on the context; IP is
			// always checked as well (and is the only key for anonymous
			// callers). Both keys are reserved in one AllowAll so a 429 on
			// either rolls the other back — an exhausted user does not spend
			// a shared IP token, and a shared-IP 429 does not spend the user
			// bucket. Different policies are different Limiter instances and
			// never share maps.
			keys := []string{"ip:" + ip}
			if user, ok := auth.UserFrom(r.Context()); ok && user.ID != "" {
				keys = append([]string{"user:" + user.ID}, keys...)
			}
			if retryAfter, ok := n.AllowAll(keys...); !ok {
				writeRateLimited(w, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	body := struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}{Error: rateLimitedMessage, Code: ErrorCode}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("ratelimit: failed to write response", "error", err)
	}
}
