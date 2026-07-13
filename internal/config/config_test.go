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

func TestLoad_allowLocalhostCORS_defaultsFalse(t *testing.T) {
	t.Setenv("ALLOW_LOCALHOST_CORS", "")

	cfg := Load()
	if cfg.AllowLocalhostCORS {
		t.Error("AllowLocalhostCORS: want false by default, got true")
	}
}

func TestLoad_allowLocalhostCORS_enabledByEnv(t *testing.T) {
	t.Setenv("ALLOW_LOCALHOST_CORS", "true")

	cfg := Load()
	if !cfg.AllowLocalhostCORS {
		t.Error("AllowLocalhostCORS: want true when ALLOW_LOCALHOST_CORS=true, got false")
	}
}
