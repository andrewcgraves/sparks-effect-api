package config

import (
	"log/slog"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestLoad_defaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("AMQP_URL", "")
	t.Setenv("ROUTING_QUEUE", "")
	t.Setenv("BOARDING_WAIT_POLICY", "")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "")

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
		t.Errorf("AMQPURL: want amqp://guest:guest@broker:5672/, got %q", cfg.AMQPURL)
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

func TestLoad_boardingWait_defaultsToNone(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitNone {
		t.Errorf("BoardingWait.Kind: want %q, got %q", transit.BoardingWaitNone, cfg.BoardingWait.Kind)
	}
}

func TestLoad_boardingWait_fromEnv(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "half_headway")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitHalfHeadway {
		t.Errorf("BoardingWait.Kind: want %q, got %q", transit.BoardingWaitHalfHeadway, cfg.BoardingWait.Kind)
	}
}

func TestLoad_boardingWait_fixedReadsItsSeconds(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "fixed")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "120")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitFixed {
		t.Fatalf("BoardingWait.Kind: want %q, got %q", transit.BoardingWaitFixed, cfg.BoardingWait.Kind)
	}
	if cfg.BoardingWait.FixedSecs != 120 {
		t.Errorf("BoardingWait.FixedSecs: want 120, got %d", cfg.BoardingWait.FixedSecs)
	}
}

func TestLoad_boardingWait_fixedWithoutSecondsFallsBackToNone(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "fixed")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitNone {
		t.Errorf("BoardingWait.Kind: want %q (default), got %q", transit.BoardingWaitNone, cfg.BoardingWait.Kind)
	}
}

// An unreadable seconds value is the same case as an absent one: fixed has no
// wait to charge, so the policy defaults rather than inventing a number.
func TestLoad_boardingWait_fixedWithUnparseableSecondsFallsBackToNone(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "fixed")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "abc")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitNone {
		t.Errorf("BoardingWait.Kind: want %q (default), got %q", transit.BoardingWaitNone, cfg.BoardingWait.Kind)
	}
	if cfg.BoardingWait.FixedSecs != 0 {
		t.Errorf("BoardingWait.FixedSecs: want 0, got %d", cfg.BoardingWait.FixedSecs)
	}
}

func TestLoad_boardingWait_negativeFixedSecondsFallsBackToNone(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "fixed")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "-30")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitNone {
		t.Errorf("BoardingWait.Kind: want %q (default), got %q", transit.BoardingWaitNone, cfg.BoardingWait.Kind)
	}
}

// The seconds companion means nothing to a headway policy, so a leftover or
// mistyped one must not cost the operator the policy they did set.
func TestLoad_boardingWait_ignoresFixedSecondsUnderAHeadwayPolicy(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "half_headway")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "abc")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitHalfHeadway {
		t.Errorf("BoardingWait.Kind: want %q, got %q", transit.BoardingWaitHalfHeadway, cfg.BoardingWait.Kind)
	}
	if cfg.BoardingWait.FixedSecs != 0 {
		t.Errorf("BoardingWait.FixedSecs: want 0, got %d", cfg.BoardingWait.FixedSecs)
	}
}

func TestLoad_boardingWait_unknownFallsBackToNone(t *testing.T) {
	t.Setenv("BOARDING_WAIT_POLICY", "not_a_policy")
	t.Setenv("BOARDING_WAIT_FIXED_SECS", "")

	cfg := Load()
	if cfg.BoardingWait.Kind != transit.BoardingWaitNone {
		t.Errorf("BoardingWait.Kind: want %q (default), got %q", transit.BoardingWaitNone, cfg.BoardingWait.Kind)
	}
}

func TestLoad_maxInFlightIsochrones_defaults(t *testing.T) {
	t.Setenv("MAX_INFLIGHT_ISOCHRONES", "")

	if got := Load().MaxInFlightIsochrones; got != defaultMaxInFlightIsochrones {
		t.Errorf("MaxInFlightIsochrones: want %d, got %d", defaultMaxInFlightIsochrones, got)
	}
}

func TestLoad_maxInFlightIsochrones_fromEnv(t *testing.T) {
	t.Setenv("MAX_INFLIGHT_ISOCHRONES", "7")

	if got := Load().MaxInFlightIsochrones; got != 7 {
		t.Errorf("MaxInFlightIsochrones: want 7, got %d", got)
	}
}

// Zero is the documented off switch, so it has to survive being read rather
// than reading as "unset" and collecting the default.
func TestLoad_maxInFlightIsochrones_zeroDisablesTheCap(t *testing.T) {
	t.Setenv("MAX_INFLIGHT_ISOCHRONES", "0")

	if got := Load().MaxInFlightIsochrones; got != 0 {
		t.Errorf("MaxInFlightIsochrones: want 0, got %d", got)
	}
}

// A typo falls back to the default rather than to "disabled": an accidentally
// uncapped queue is the failure nobody notices until a flood.
func TestLoad_maxInFlightIsochrones_malformedKeepsTheDefault(t *testing.T) {
	for _, v := range []string{"lots", "-1", "3.5"} {
		t.Setenv("MAX_INFLIGHT_ISOCHRONES", v)

		if got := Load().MaxInFlightIsochrones; got != defaultMaxInFlightIsochrones {
			t.Errorf("MAX_INFLIGHT_ISOCHRONES=%q: want %d, got %d", v, defaultMaxInFlightIsochrones, got)
		}
	}
}
