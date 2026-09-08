package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

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
	BoardingWait           transit.BoardingWaitPolicy
	WorkerToken            string
	PasswordHashCost       int
}

const defaultSessionTTL = 24 * time.Hour

const defaultRoutingQueue = "routing.jobs"

const defaultMaxInFlightIsochrones = 20

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

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
