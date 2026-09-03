// Package config loads runtime configuration from the environment.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	// Config depends on a domain type only to hand every caller one validated
	// BoardingWaitPolicy instead of a raw kind/seconds pair. Deliberate for
	// SPA-236; if config grows more domain imports, parse in main instead.
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
	// MaxInFlightIsochrones caps how many routing jobs may be queued or running
	// before POST /api/isochrone and the two authored isochrone endpoints start
	// refusing with 429 (SPA-219). Set MAX_INFLIGHT_ISOCHRONES to override the
	// default; 0 disables the cap.
	MaxInFlightIsochrones int
	// BoardingWait is the global boarding-wait policy compiled into every
	// ServiceGraph.WaitSecs (SPA-236). Set BOARDING_WAIT_POLICY to none (the
	// default), half_headway, full_headway, or fixed. fixed also requires
	// BOARDING_WAIT_FIXED_SECS (≥ 0), which is read only under that policy.
	// A malformed or incomplete setting falls back to none — same soft-default
	// pattern as other env knobs; see loadBoardingWait.
	BoardingWait transit.BoardingWaitPolicy
	// PasswordHashCost is the bcrypt cost new passwords are hashed at. Zero —
	// which is what Load always leaves it as — means auth's default cost.
	//
	// Deliberately not read from the environment. Its only caller is the test
	// suite, which sets bcrypt.MinCost so that provisioning and logging in
	// dozens of accounts does not cost dozens of deliberately-slow hashes. An
	// env knob would put that same lever within reach of a production deploy,
	// where a mistyped value silently weakens every stored password; a field
	// the loader never populates cannot be set by accident.
	PasswordHashCost int
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

// defaultMaxInFlightIsochrones is the ceiling on queued-or-running routing jobs
// applied when MAX_INFLIGHT_ISOCHRONES is unset.
//
// Derived from what the backlog can actually drain rather than picked round.
// The routing worker plots one chain at a time (broker prefetch is 1) and a
// chain over a warm scenario takes a few seconds, so the 90 seconds after which
// handler.RoutingJobStaleAfter gives up on a job clears something in the region
// of twenty of them. A cap there means a full backlog is still one a live
// worker works through inside the window a client is prepared to wait — while a
// caller enqueueing at HTTP speed hits it almost immediately and is refused
// from then on.
const defaultMaxInFlightIsochrones = 20

// Load reads configuration from environment variables, applying defaults
// for anything unset.
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
	}
}

// loadBoardingWait reads BOARDING_WAIT_POLICY / BOARDING_WAIT_FIXED_SECS.
// Unset → none. Malformed or incomplete values also resolve to none.
//
// SPA-236 originally asked for a malformed setting to be rejected at load time;
// the soft default is a deliberate departure from that acceptance criterion. It
// keeps both failure modes of a typo cheap: the process still boots, and the
// worst a mistyped policy can do is charge no wait rather than an invented one.
//
// The seconds companion is read only under the fixed policy, the one kind that
// gives it meaning, so a stale or garbage BOARDING_WAIT_FIXED_SECS left next to
// half_headway cannot discard an otherwise valid policy.
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

// loadMaxInFlightIsochrones reads MAX_INFLIGHT_ISOCHRONES. Unset falls back to
// the default; an explicit 0 disables the cap, which is why this cannot reuse
// the `n > 0` shape the other integer settings share.
//
// A malformed or negative value falls back to the default rather than to
// "disabled", and says so. Getting this wrong in the direction of no cap is the
// failure that is invisible until an actual flood, so a typo lands on the safe
// side and the operator still sees why.
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
