// Package config loads runtime configuration from the environment.
package config

import (
	"os"
	"strconv"
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
}

// Load reads configuration from environment variables, applying defaults
// for anything unset.
func Load() Config {
	maxConns := 0
	if v := os.Getenv("DATABASE_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConns = n
		}
	}
	return Config{
		Port:               getEnv("PORT", "8080"),
		StadiaAPIKey:       os.Getenv("STADIA_API_KEY"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		DBMaxConns:         maxConns,
		Debug:              os.Getenv("LOG_LEVEL") == "debug" || os.Getenv("VERBOSE") == "true",
		AllowLocalhostCORS: os.Getenv("ALLOW_LOCALHOST_CORS") == "true",
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
