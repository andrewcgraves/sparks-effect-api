package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type RateLimitPolicy struct {
	RatePerMinute int
	Burst         int
}

type Config struct {
	Port                   string
	AMQPURL                string
	RoutingQueue           string
	DatabaseURL            string
	DBMaxConns             int
	LogLevel               slog.Level
	AllowLocalhostCORS     bool
	SessionTTL             time.Duration
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
	MaxInFlightIsochrones  int
	TrustedProxyCount      int
	RateLimitIsochrone     RateLimitPolicy
	RateLimitSnapStops     RateLimitPolicy
	RateLimitLogin         RateLimitPolicy
	RateLimitCompile       RateLimitPolicy
	BoardingWait           transit.BoardingWaitPolicy
	WorkerToken            string
	PasswordHashCost       int
}

const defaultSessionTTL = 24 * time.Hour

const defaultRoutingQueue = "routing.jobs"

const defaultMaxInFlightIsochrones = 20

const defaultTrustedProxyCount = 1

var (
	defaultRateLimitIsochrone = RateLimitPolicy{RatePerMinute: 10, Burst: 5}
	defaultRateLimitSnapStops = RateLimitPolicy{RatePerMinute: 30, Burst: 10}
	defaultRateLimitLogin     = RateLimitPolicy{RatePerMinute: 5, Burst: 5}
	defaultRateLimitCompile   = RateLimitPolicy{RatePerMinute: 10, Burst: 3}
)

func Load() Config {
	maxConns := 0
	if v := os.Getenv("DATABASE_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConns = n
		}
	}
	sessionTTL := defaultSessionTTL
	if v := os.Getenv("SESSION_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sessionTTL = time.Duration(n) * time.Hour
		}
	}

	logLevel := logger.ParseLevel(os.Getenv("LOG_LEVEL"))
	if os.Getenv("VERBOSE") == "true" {
		logLevel = slog.LevelDebug
	}

	return Config{
		Port:                   getEnv("PORT", "8080"),
		AMQPURL:                os.Getenv("AMQP_URL"),
		RoutingQueue:           getEnv("ROUTING_QUEUE", defaultRoutingQueue),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		DBMaxConns:             maxConns,
		LogLevel:               logLevel,
		AllowLocalhostCORS:     os.Getenv("ALLOW_LOCALHOST_CORS") == "true",
		SessionTTL:             sessionTTL,
		BootstrapAdminEmail:    os.Getenv("BOOTSTRAP_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		MaxInFlightIsochrones:  loadMaxInFlightIsochrones(),
		TrustedProxyCount:      loadTrustedProxyCount(),
		RateLimitIsochrone:     loadRateLimit("ISOCHRONE", defaultRateLimitIsochrone),
		RateLimitSnapStops:     loadRateLimit("SNAP_STOPS", defaultRateLimitSnapStops),
		RateLimitLogin:         loadRateLimit("LOGIN", defaultRateLimitLogin),
		RateLimitCompile:       loadRateLimit("COMPILE", defaultRateLimitCompile),
		BoardingWait:           loadBoardingWait(),
		WorkerToken:            os.Getenv("WORKER_TOKEN"),
	}
}

func loadBoardingWait() transit.BoardingWaitPolicy {
	kind := os.Getenv("BOARDING_WAIT_POLICY")
	var fixed *int
	if transit.BoardingWaitKind(kind) == transit.BoardingWaitFixed {
		if n, err := strconv.Atoi(os.Getenv("BOARDING_WAIT_FIXED_SECS")); err == nil {
			fixed = &n
		}
	}
	p, err := transit.ParseBoardingWaitPolicy(kind, fixed)
	if err != nil {
		// Warned rather than swallowed: the whole point of booting anyway is
		// that the operator can fix the typo, which needs them to see it.
		slog.Warn("config: BOARDING_WAIT_POLICY ignored, charging no boarding wait",
			"boarding_wait_policy", kind, "error", err)
		return transit.DefaultBoardingWaitPolicy()
	}
	return p
}

func loadMaxInFlightIsochrones() int {
	v := os.Getenv("MAX_INFLIGHT_ISOCHRONES")
	if v == "" {
		return defaultMaxInFlightIsochrones
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Warn("config: MAX_INFLIGHT_ISOCHRONES ignored, keeping the default cap",
			"max_inflight_isochrones", v, "default", defaultMaxInFlightIsochrones)
		return defaultMaxInFlightIsochrones
	}
	return n
}

func loadTrustedProxyCount() int {
	v := os.Getenv("TRUSTED_PROXY_COUNT")
	if v == "" {
		return defaultTrustedProxyCount
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Warn("config: TRUSTED_PROXY_COUNT ignored, keeping the default",
			"trusted_proxy_count", v, "default", defaultTrustedProxyCount)
		return defaultTrustedProxyCount
	}
	return n
}

func loadRateLimit(name string, fallback RateLimitPolicy) RateLimitPolicy {
	return RateLimitPolicy{
		RatePerMinute: loadNonNegativeEnv("RATE_LIMIT_"+name+"_PER_MIN", fallback.RatePerMinute),
		Burst:         loadBurstEnv("RATE_LIMIT_"+name+"_BURST", fallback.Burst),
	}
}

func loadNonNegativeEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Warn("config: "+key+" ignored, keeping the default",
			"value", v, "default", fallback)
		return fallback
	}
	return n
}

func loadBurstEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	// Only RATE_LIMIT_*_PER_MIN=0 disables a limiter. A zero burst is ignored
	// like a malformed value so RATE_LIMIT_LOGIN_BURST=0 cannot fail-open.
	if err != nil || n <= 0 {
		slog.Warn("config: "+key+" ignored, keeping the default",
			"value", v, "default", fallback)
		return fallback
	}
	return n
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
