// Package config loads runtime configuration from the environment.
package config

import "os"

// Config holds the settings needed to run the API server.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port string
}

// Load reads configuration from environment variables, applying defaults
// for anything unset.
func Load() Config {
	return Config{
		Port: getEnv("PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
