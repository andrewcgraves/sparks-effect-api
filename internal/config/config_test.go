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
	t.Setenv("WORKER_TOKEN", "")
	clearRateLimitEnv(t)

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
	if cfg.WorkerToken != "" {
		t.Errorf("WorkerToken: want empty, got %q", cfg.WorkerToken)
	}
	if cfg.TrustedProxyCount != defaultTrustedProxyCount {
		t.Errorf("TrustedProxyCount: want %d, got %d", defaultTrustedProxyCount, cfg.TrustedProxyCount)
	}
	assertPolicy(t, "Isochrone", cfg.RateLimitIsochrone, defaultRateLimitIsochrone)
	assertPolicy(t, "SnapStops", cfg.RateLimitSnapStops, defaultRateLimitSnapStops)
	assertPolicy(t, "Login", cfg.RateLimitLogin, defaultRateLimitLogin)
	assertPolicy(t, "Compile", cfg.RateLimitCompile, defaultRateLimitCompile)
}

func TestLoad_fromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("AMQP_URL", "amqp://guest:guest@broker:5672/")
	t.Setenv("ROUTING_QUEUE", "custom.queue")
	t.Setenv("WORKER_TOKEN", "shared-secret")

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
	if cfg.WorkerToken != "shared-secret" {
		t.Errorf("WorkerToken: want shared-secret, got %q", cfg.WorkerToken)
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

func TestLoad_maxInFlightIsochrones_zeroDisablesTheCap(t *testing.T) {
	t.Setenv("MAX_INFLIGHT_ISOCHRONES", "0")

	if got := Load().MaxInFlightIsochrones; got != 0 {
		t.Errorf("MaxInFlightIsochrones: want 0, got %d", got)
	}
}

func TestLoad_maxInFlightIsochrones_malformedKeepsTheDefault(t *testing.T) {
	for _, v := range []string{"lots", "-1", "3.5"} {
		t.Setenv("MAX_INFLIGHT_ISOCHRONES", v)

		if got := Load().MaxInFlightIsochrones; got != defaultMaxInFlightIsochrones {
			t.Errorf("MAX_INFLIGHT_ISOCHRONES=%q: want %d, got %d", v, defaultMaxInFlightIsochrones, got)
		}
	}
}

func TestLoad_trustedProxyCount_defaultsToOne(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_COUNT", "")

	if got := Load().TrustedProxyCount; got != defaultTrustedProxyCount {
		t.Errorf("TrustedProxyCount: want %d, got %d", defaultTrustedProxyCount, got)
	}
}

func TestLoad_trustedProxyCount_fromEnv(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_COUNT", "2")

	if got := Load().TrustedProxyCount; got != 2 {
		t.Errorf("TrustedProxyCount: want 2, got %d", got)
	}
}

func TestLoad_trustedProxyCount_zeroIsAccepted(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_COUNT", "0")

	if got := Load().TrustedProxyCount; got != 0 {
		t.Errorf("TrustedProxyCount: want 0, got %d", got)
	}
}

func TestLoad_trustedProxyCount_malformedKeepsTheDefault(t *testing.T) {
	for _, v := range []string{"lots", "-1", "3.5"} {
		t.Setenv("TRUSTED_PROXY_COUNT", v)

		if got := Load().TrustedProxyCount; got != defaultTrustedProxyCount {
			t.Errorf("TRUSTED_PROXY_COUNT=%q: want %d, got %d", v, defaultTrustedProxyCount, got)
		}
	}
}

func TestLoad_rateLimits_fromEnv(t *testing.T) {
	t.Setenv("RATE_LIMIT_ISOCHRONE_PER_MIN", "7")
	t.Setenv("RATE_LIMIT_ISOCHRONE_BURST", "2")
	t.Setenv("RATE_LIMIT_SNAP_STOPS_PER_MIN", "11")
	t.Setenv("RATE_LIMIT_SNAP_STOPS_BURST", "4")
	t.Setenv("RATE_LIMIT_LOGIN_PER_MIN", "3")
	t.Setenv("RATE_LIMIT_LOGIN_BURST", "1")
	t.Setenv("RATE_LIMIT_COMPILE_PER_MIN", "9")
	t.Setenv("RATE_LIMIT_COMPILE_BURST", "6")

	cfg := Load()
	assertPolicy(t, "Isochrone", cfg.RateLimitIsochrone, RateLimitPolicy{RatePerMinute: 7, Burst: 2})
	assertPolicy(t, "SnapStops", cfg.RateLimitSnapStops, RateLimitPolicy{RatePerMinute: 11, Burst: 4})
	assertPolicy(t, "Login", cfg.RateLimitLogin, RateLimitPolicy{RatePerMinute: 3, Burst: 1})
	assertPolicy(t, "Compile", cfg.RateLimitCompile, RateLimitPolicy{RatePerMinute: 9, Burst: 6})
}

func TestLoad_rateLimits_zeroDisables(t *testing.T) {
	t.Setenv("RATE_LIMIT_ISOCHRONE_PER_MIN", "0")
	t.Setenv("RATE_LIMIT_ISOCHRONE_BURST", "0")
	t.Setenv("RATE_LIMIT_LOGIN_PER_MIN", "0")

	cfg := Load()
	if cfg.RateLimitIsochrone.RatePerMinute != 0 || cfg.RateLimitIsochrone.Burst != 0 {
		t.Errorf("Isochrone: want zero policy, got %+v", cfg.RateLimitIsochrone)
	}
	if cfg.RateLimitLogin.RatePerMinute != 0 {
		t.Errorf("Login.RatePerMinute: want 0, got %d", cfg.RateLimitLogin.RatePerMinute)
	}
}

func TestLoad_rateLimits_malformedKeepsTheDefault(t *testing.T) {
	for _, v := range []string{"lots", "-1", "3.5"} {
		t.Setenv("RATE_LIMIT_ISOCHRONE_PER_MIN", v)
		t.Setenv("RATE_LIMIT_ISOCHRONE_BURST", v)

		cfg := Load()
		assertPolicy(t, "Isochrone malformed "+v, cfg.RateLimitIsochrone, defaultRateLimitIsochrone)
	}
}

func clearRateLimitEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TRUSTED_PROXY_COUNT",
		"RATE_LIMIT_ISOCHRONE_PER_MIN", "RATE_LIMIT_ISOCHRONE_BURST",
		"RATE_LIMIT_SNAP_STOPS_PER_MIN", "RATE_LIMIT_SNAP_STOPS_BURST",
		"RATE_LIMIT_LOGIN_PER_MIN", "RATE_LIMIT_LOGIN_BURST",
		"RATE_LIMIT_COMPILE_PER_MIN", "RATE_LIMIT_COMPILE_BURST",
	} {
		t.Setenv(key, "")
	}
}

func assertPolicy(t *testing.T, name string, got, want RateLimitPolicy) {
	t.Helper()
	if got != want {
		t.Errorf("%s: want %+v, got %+v", name, want, got)
	}
}
