// Package config loads runtime configuration from the environment.
package config

import "os"

// Config holds the settings needed to run the API server.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port string
	// StadiaAPIKey is the Stadia Maps API key for routing calls.
	StadiaAPIKey string
}

// Load reads configuration from environment variables, applying defaults
// for anything unset.
func Load() Config {
	return Config{
		Port:         getEnv("PORT", "8080"),
		StadiaAPIKey: os.Getenv("STADIA_API_KEY"),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
