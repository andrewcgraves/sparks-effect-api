// Package config loads runtime configuration from the environment.
package config

import "os"

// Config holds the settings needed to run the API server.
type Config struct {
	Port         string
	StadiaAPIKey string
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
	return Config{
		Port:               getEnv("PORT", "8080"),
		StadiaAPIKey:       os.Getenv("STADIA_API_KEY"),
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
