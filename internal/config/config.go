// Package config loads runtime configuration from the environment.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds the settings needed to run the API server.
type Config struct {
	Port         string
	StadiaAPIKey string
	// DatabaseURL is the Postgres connection string (Railway injects DATABASE_URL
	// via the private-network endpoint). When empty the server falls back to the
	// read-only embedded YAML store, which keeps local dev usable without a DB.
	DatabaseURL string
	// DBMaxConns caps the pgx connection pool size when > 0. Set DATABASE_MAX_CONNS
	// to override; 0 leaves pgx's default (derived from CPU count) in place.
	DBMaxConns int
	// Debug enables verbose request/response logging. Set LOG_LEVEL=debug or
	// VERBOSE=true to enable. Never logs secret values.
	Debug bool
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
}

// defaultSessionTTL bounds how long a stolen token stays useful. A day is short
// enough to limit exposure and long enough not to interrupt a working session.
const defaultSessionTTL = 24 * time.Hour

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

	return Config{
		Port:                   getEnv("PORT", "8080"),
		StadiaAPIKey:           os.Getenv("STADIA_API_KEY"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		DBMaxConns:             maxConns,
		Debug:                  os.Getenv("LOG_LEVEL") == "debug" || os.Getenv("VERBOSE") == "true",
		AllowLocalhostCORS:     os.Getenv("ALLOW_LOCALHOST_CORS") == "true",
		SessionTTL:             sessionTTL,
		BootstrapAdminEmail:    os.Getenv("BOOTSTRAP_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
