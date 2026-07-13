package config

import "testing"

func TestLoad_defaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("STADIA_API_KEY", "")

	cfg := Load()
	if cfg.Port != "8080" {
		t.Errorf("Port: want 8080, got %q", cfg.Port)
	}
	if cfg.StadiaAPIKey != "" {
		t.Errorf("StadiaAPIKey: want empty, got %q", cfg.StadiaAPIKey)
	}
}

func TestLoad_fromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("STADIA_API_KEY", "test-key")

	cfg := Load()
	if cfg.Port != "9090" {
		t.Errorf("Port: want 9090, got %q", cfg.Port)
	}
	if cfg.StadiaAPIKey != "test-key" {
		t.Errorf("StadiaAPIKey: want test-key, got %q", cfg.StadiaAPIKey)
	}
}
