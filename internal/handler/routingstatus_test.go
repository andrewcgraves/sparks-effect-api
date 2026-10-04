package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

type fakeQueueStore struct {
	queue  handler.RoutingQueue
	err    error
	window time.Duration
}

func (f *fakeQueueStore) RoutingQueueSnapshot(_ context.Context, within time.Duration) (handler.RoutingQueue, error) {
	f.window = within
	return f.queue, f.err
}

type routingStatusBody struct {
	Status           string `json:"status"`
	OldestQueuedSecs int    `json:"oldest_queued_secs"`
	InFlight         int    `json:"inflight"`
}

func getRoutingStatus(t *testing.T, store handler.RoutingQueueStore, watch *handler.WorkerWatch) (*httptest.ResponseRecorder, routingStatusBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.RoutingStatus(store, watch).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/routing/status", nil))
	var body routingStatusBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func TestRoutingStatusIsOKWithAnEmptyQueue(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)

	rec, body := getRoutingStatus(t, &fakeQueueStore{}, watch)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body != (routingStatusBody{Status: "ok"}) {
		t.Errorf("body = %+v, want ok with nothing queued", body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "max-age=10" {
		t.Errorf("Cache-Control = %q, want max-age=10", got)
	}
}

func TestRoutingStatusIsDegradedWhenAJobWaitsWhileTheWorkerIsInContact(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	store := &fakeQueueStore{queue: handler.RoutingQueue{
		InFlight: 3, OldestQueuedAt: clock.t.Add(-61 * time.Second),
	}}

	_, body := getRoutingStatus(t, store, watch)

	if body != (routingStatusBody{Status: "degraded", OldestQueuedSecs: 61, InFlight: 3}) {
		t.Errorf("body = %+v, want degraded with the 61 s wait and 3 in flight", body)
	}
	if store.window != handler.RoutingJobStaleAfter {
		t.Errorf("queue read over %s, want the in-flight window %s", store.window, handler.RoutingJobStaleAfter)
	}
}

func TestRoutingStatusIsOKWhileTheOldestJobHasWaitedAMinuteOrLess(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	clock.advance(5 * time.Minute)
	store := &fakeQueueStore{queue: handler.RoutingQueue{
		InFlight: 1, OldestQueuedAt: clock.t.Add(-60 * time.Second),
	}}

	_, body := getRoutingStatus(t, store, watch)

	if body.Status != "ok" || body.OldestQueuedSecs != 60 {
		t.Errorf("body = %+v, want ok at exactly 60 s, however long the worker has been silent", body)
	}
}

func contactFromWorker(t *testing.T, watch *handler.WorkerWatch) {
	t.Helper()
	h := watch.RecordContact(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/internal/worker", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("RecordContact did not pass the request on: %d", rec.Code)
	}
}

func TestRoutingStatusIsOfflineWhenTheWorkerIsSilentAndAJobWaits(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	contactFromWorker(t, watch)
	clock.advance(2 * time.Minute)
	store := &fakeQueueStore{queue: handler.RoutingQueue{
		InFlight: 1, OldestQueuedAt: clock.t.Add(-61 * time.Second),
	}}

	if _, body := getRoutingStatus(t, store, watch); body.Status != "offline" {
		t.Fatalf("status = %q, want offline after 2 min of silence with a 61 s wait", body.Status)
	}

	// Any authenticated worker request is proof of life. The job still waits,
	// so the worker is back but behind.
	contactFromWorker(t, watch)
	if _, body := getRoutingStatus(t, store, watch); body.Status != "degraded" {
		t.Errorf("status = %q after contact, want degraded", body.Status)
	}
}

func TestRoutingStatusIsOKWhenTheWorkerIsSilentButNothingWaits(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	clock.advance(time.Hour)

	if _, body := getRoutingStatus(t, &fakeQueueStore{}, watch); body.Status != "ok" {
		t.Errorf("status = %q, want ok: silence alone is an idle worker, not a dead one", body.Status)
	}
}

// A browser polling its job has it marked failed at RoutingJobStaleAfter, so
// the queue alone stops showing the wait long before two minutes of silence.
// What the API published and the worker never answered still does.
func TestRoutingStatusIsOfflineWhenAPublishedJobWasNeverAnswered(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	pub := watch.Publisher(&routing.FakePublisher{})
	empty := &fakeQueueStore{}

	clock.advance(30 * time.Second)
	if err := pub.Publish(context.Background(), routing.Message{RoutingJobID: "job-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	clock.advance(30 * time.Second)
	if err := pub.Publish(context.Background(), routing.Message{RoutingJobID: "job-2"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	clock.advance(time.Minute)
	if _, body := getRoutingStatus(t, empty, watch); body.Status != "offline" {
		t.Fatalf("status = %q, want offline: the first job has gone 90 s unanswered and the worker 2 min silent", body.Status)
	}

	contactFromWorker(t, watch)
	clock.advance(time.Hour)
	if _, body := getRoutingStatus(t, empty, watch); body.Status != "ok" {
		t.Errorf("status = %q, want ok: contact after the publish answers it", body.Status)
	}
}

func TestRoutingStatusIgnoresAPublishThatFailed(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	watch := handler.NewWorkerWatch(clock.now)
	pub := watch.Publisher(&routing.FakePublisher{Err: errors.New("broker down")})

	if err := pub.Publish(context.Background(), routing.Message{}); err == nil {
		t.Fatal("Publish swallowed the broker's error")
	}
	clock.advance(time.Hour)

	if _, body := getRoutingStatus(t, &fakeQueueStore{}, watch); body.Status != "ok" {
		t.Errorf("status = %q, want ok: an unpublished job never reached the worker", body.Status)
	}
}

func TestRoutingStatusReportsAQueueReadFailure(t *testing.T) {
	watch := handler.NewWorkerWatch(time.Now)
	rec, _ := getRoutingStatus(t, &fakeQueueStore{err: errors.New("db gone")}, watch)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want 500", rec.Code)
	}
}

func TestRoutingStatusAnswersNothingButTheThreeFields(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	store := &fakeQueueStore{queue: handler.RoutingQueue{InFlight: 2, OldestQueuedAt: clock.t.Add(-5 * time.Second)}}
	rec, _ := getRoutingStatus(t, store, handler.NewWorkerWatch(clock.now))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{"status": "ok", "oldest_queued_secs": float64(5), "inflight": float64(2)}
	if len(raw) != len(want) {
		t.Fatalf("body = %v, want exactly %v: it is public, so no job id, origin or owner", raw, want)
	}
	for k, v := range want {
		if raw[k] != v {
			t.Errorf("%s = %v, want %v", k, raw[k], v)
		}
	}
}
