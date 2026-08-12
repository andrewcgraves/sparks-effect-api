// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Config holds the settings needed to run the API server.
type Config struct {
	Port string
	// AMQPURL is the RabbitMQ connection string the isochrone endpoints publish
	// routing jobs to. When empty they answer 503, the same way the
	// database-backed routes do without DATABASE_URL: the API is still useful
	// for everything that does not need the routing worker, and local dev
	// should not require a broker to run.
	AMQPURL string
	// RoutingQueue is the queue routing jobs are published to. It must match
	// what the worker consumes from — one of the few strings shared across the
	// repository boundary, hence a setting rather than a constant.
	RoutingQueue string
	// DatabaseURL is the Postgres connection string (Railway injects DATABASE_URL
	// via the private-network endpoint). When empty the server falls back to the
	// read-only embedded YAML store, which keeps local dev usable without a DB.
	DatabaseURL string
	// DBMaxConns caps the pgx connection pool size when > 0. Set DATABASE_MAX_CONNS
	// to override; 0 leaves pgx's default (derived from CPU count) in place.
	DBMaxConns int
	// LogLevel gates what the API logs, as one of "debug", "info", "warn", or
	// "error" (case-insensitive), set via LOG_LEVEL. VERBOSE=true is a back-
	// compat alias for LOG_LEVEL=debug. Unset or unrecognised falls back to
	// Info. Never logs secret values regardless of level.
	LogLevel slog.Level
	// AllowLocalhostCORS enables CORS headers for localhost origins.
	// Set ALLOW_LOCALHOST_CORS=true for local SPA testing only. Off by default.
	AllowLocalhostCORS bool
	// SessionTTL is how long a session token stays valid after login. Set
	// SESSION_TTL_HOURS to override the 24-hour default.
	SessionTTL time.Duration
	// BootstrapAdminEmail and BootstrapAdminPassword provision the first admin
	// account on boot. An invite-only API has no signup path, so without this
	// there would be no way to create the account that creates the accounts.
	// Applied only when the email does not already exist; leave unset once the
	// admin is established.
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
	// BoardingWait is the global boarding-wait policy compiled into every
	// ServiceGraph.WaitSecs (SPA-236). Set BOARDING_WAIT_POLICY to none (the
	// default), half_headway, full_headway, or fixed. fixed also requires
	// BOARDING_WAIT_FIXED_SECS (≥ 0); an incomplete pair is a Load error.
	BoardingWait transit.BoardingWaitPolicy
}

// defaultSessionTTL bounds how long a stolen token stays useful. A day is short
// enough to limit exposure and long enough not to interrupt a working session.
const defaultSessionTTL = 24 * time.Hour

// defaultRoutingQueue is the queue name both sides of the routing contract use
// unless deliberately overridden. Defaulted rather than required because
// getting it wrong is silent — the API publishes happily to a queue nothing
// consumes — so the two repositories agreeing by default is worth more than
// forcing the operator to set it.
const defaultRoutingQueue = "routing.jobs"

// Load reads configuration from environment variables, applying defaults
// for anything unset. A malformed boarding-wait policy is a hard error: the
// process must not boot with a silently remapped non-zero wait.
func Load() (Config, error) {
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

	boardingWait, err := loadBoardingWait()
	if err != nil {
		return Config{}, err
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
		BoardingWait:           boardingWait,
	}, nil
}

func loadBoardingWait() (transit.BoardingWaitPolicy, error) {
	kind := os.Getenv("BOARDING_WAIT_POLICY")
	var fixed *int
	if v := os.Getenv("BOARDING_WAIT_FIXED_SECS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return transit.BoardingWaitPolicy{}, fmt.Errorf("config: BOARDING_WAIT_FIXED_SECS: %w", err)
		}
		fixed = &n
	}
	p, err := transit.ParseBoardingWaitPolicy(kind, fixed)
	if err != nil {
		return transit.BoardingWaitPolicy{}, fmt.Errorf("config: %w", err)
	}
	return p, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
