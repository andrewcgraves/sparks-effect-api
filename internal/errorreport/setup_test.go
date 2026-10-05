package errorreport_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewcgraves/sparks-effect-api/internal/errorreport"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

func TestSetupWithoutAnEndpointDisablesReportingWithOneInfoLine(t *testing.T) {
	var logs bytes.Buffer
	rep, shutdown, err := errorreport.Setup(context.Background(), false, "abc123", logger.New(&logs, 0))
	if err != nil {
		t.Fatalf("Setup() error = %v, want nil: an unset endpoint is not a boot failure", err)
	}
	if rep != nil {
		t.Errorf("Setup() = %v, want a nil Reporter when reporting is disabled", rep)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"level":"INFO"`) || !strings.Contains(lines[0], "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("logs = %q, want one info line naming OTEL_EXPORTER_OTLP_ENDPOINT", logs.String())
	}
}

func TestSetupExportsReportsTaggedWithReleaseAndEnvironment(t *testing.T) {
	var mu sync.Mutex
	var got []*collogs.ExportLogsServiceRequest
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := &collogs.ExportLogsServiceRequest{}
		if r.URL.Path != "/v1/logs" || proto.Unmarshal(body, req) != nil {
			t.Errorf("collector got %s %s, want an OTLP logs export", r.Method, r.URL.Path)
		}
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.namespace=staging")

	rep, shutdown, err := errorreport.Setup(context.Background(), true, "abc123", logger.Discard())
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	h := rep.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorreport.Capture(r.Context(), "op", errors.New("boom"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || len(got[0].ResourceLogs) == 0 {
		t.Fatal("the collector received no logs; shutdown should flush the report")
	}
	res := map[string]string{}
	for _, kv := range got[0].ResourceLogs[0].Resource.Attributes {
		res[kv.Key] = kv.Value.GetStringValue()
	}
	want := map[string]string{
		"service.name":      "sparks-effect-api",
		"service.namespace": "staging",
		"service.version":   "abc123",
	}
	for k, v := range want {
		if res[k] != v {
			t.Errorf("resource %s = %q, want %q", k, res[k], v)
		}
	}
}
