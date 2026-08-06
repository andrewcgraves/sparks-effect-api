package config

import (
	"log/slog"
	"testing"
)

func TestLoad_defaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("AMQP_URL", "")
	t.Setenv("ROUTING_QUEUE", "")

	cfg := Load()
	if cfg.Port != "8080" {
		t.Errorf("Port: want 8080, got %q", cfg.Port)
	}
	// Empty is the signal the isochrone routes read to register themselves as
	// 503s, so it must not acquire a default the way the queue name does.
	if cfg.AMQPURL != "" {
		t.Errorf("AMQPURL: want empty, got %q", cfg.AMQPURL)
	}
	// The queue name, by contrast, defaults — both repositories agreeing on it
	// without configuration is worth more than forcing it to be set, because
	// getting it wrong is silent.
	if cfg.RoutingQueue != "routing.jobs" {
		t.Errorf("RoutingQueue: want routing.jobs, got %q", cfg.RoutingQueue)
	}
}

func TestLoad_fromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("AMQP_URL", "amqp://guest:guest@broker:5672/")
	t.Setenv("ROUTING_QUEUE", "custom.queue")

	cfg := Load()
	if cfg.Port != "9090" {
		t.Errorf("Port: want 9090, got %q", cfg.Port)
	}
	if cfg.AMQPURL != "amqp://guest:guest@broker:5672/" {
		t.Errorf("AMQPURL: got %q", cfg.AMQPURL)
	}
	if cfg.RoutingQueue != "custom.queue" {
		t.Errorf("RoutingQueue: want custom.queue, got %q", cfg.RoutingQueue)
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

func TestLoad_logLevel_defaultsToInfo(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("VERBOSE", "")

	if cfg := Load(); cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel: want %v, got %v", slog.LevelInfo, cfg.LogLevel)
	}
}

func TestLoad_logLevel_fromEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("VERBOSE", "")

	if cfg := Load(); cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel: want %v, got %v", slog.LevelDebug, cfg.LogLevel)
	}
}

// VERBOSE=true predates LOG_LEVEL and stays supported so existing deploy
// configs keep working unchanged.
func TestLoad_logLevel_verboseIsABackCompatAliasForDebug(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("VERBOSE", "true")

	if cfg := Load(); cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel: want %v when VERBOSE=true, got %v", slog.LevelDebug, cfg.LogLevel)
	}
}
