package metrics_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/metrics"
)

func TestSetupWithoutAnEndpointDisablesExportWithOneInfoLine(t *testing.T) {
	var logs bytes.Buffer
	m, shutdown, err := metrics.Setup(context.Background(), false, logger.New(&logs, 0))
	if err != nil {
		t.Fatalf("Setup() error = %v, want nil: an unset endpoint is not a boot failure", err)
	}
	if m != nil {
		t.Errorf("Setup() = %v, want nil Metrics when export is disabled", m)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"level":"INFO"`) || !strings.Contains(lines[0], "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("logs = %q, want one info line naming OTEL_EXPORTER_OTLP_ENDPOINT", logs.String())
	}
}

func TestSetupPushesToTheConfiguredEndpoint(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	m, shutdown, err := metrics.Setup(context.Background(), true, logger.Discard())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if m == nil {
		t.Fatal("Setup() returned nil Metrics with export enabled")
	}
	m.BacklogFull(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[len(paths)-1] != "POST /v1/metrics" {
		t.Errorf("collector saw %v, want a final POST /v1/metrics flushed on shutdown", paths)
	}
}
