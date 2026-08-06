package routing

import "testing"

// The worker reads a job's trace id off the AMQP delivery's x-trace-id
// header (see traceIDFromDelivery in that repository's internal/worker
// package), so this is the transport that actually reaches it — the
// message's own trace_id JSON field is not parsed by the worker at all.
func TestTraceHeaders(t *testing.T) {
	tests := []struct {
		name    string
		traceID string
		wantOK  bool
	}{
		{"present", "trace-abc-123", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := traceHeaders(tt.traceID)
			got, ok := headers[amqpTraceIDHeader]
			if ok != tt.wantOK {
				t.Fatalf("header present = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.traceID {
				t.Errorf("header = %v, want %v", got, tt.traceID)
			}
		})
	}
}
