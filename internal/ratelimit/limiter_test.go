package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterAllowUnderBurst(t *testing.T) {
	l := New(60, 3)
	for i := 0; i < 3; i++ {
		if _, ok := l.Allow("ip:1.1.1.1"); !ok {
			t.Fatalf("request %d denied, burst is 3", i+1)
		}
	}
}

func TestLimiterAllowOverBurst(t *testing.T) {
	l := New(1, 1)
	if _, ok := l.Allow("ip:1.1.1.1"); !ok {
		t.Fatal("first request denied")
	}
	retry, ok := l.Allow("ip:1.1.1.1")
	if ok {
		t.Fatal("second request allowed, burst is 1")
	}
	if retry < time.Second {
		t.Errorf("Retry-After %v, want at least 1s", retry)
	}
}

func TestLimiterIndependentKeys(t *testing.T) {
	l := New(1, 1)
	if _, ok := l.Allow("ip:1.1.1.1"); !ok {
		t.Fatal("first IP denied")
	}
	if _, ok := l.Allow("ip:2.2.2.2"); !ok {
		t.Fatal("second IP shared the first IP's bucket")
	}
}

func TestLimiterSameKeySharesBucket(t *testing.T) {
	l := New(1, 1)
	if _, ok := l.Allow("ip:1.1.1.1"); !ok {
		t.Fatal("first request denied")
	}
	if _, ok := l.Allow("ip:1.1.1.1"); ok {
		t.Fatal("same key did not share the bucket")
	}
}

func TestLimiterNilAndZeroArePassThrough(t *testing.T) {
	for _, l := range []*Limiter{nil, New(0, 5), New(10, 0), New(-1, 5)} {
		for i := 0; i < 20; i++ {
			if _, ok := l.Allow("ip:1.1.1.1"); !ok {
				t.Fatalf("disabled limiter %v denied request %d", l, i)
			}
		}
	}
}

func TestLimiterConcurrentAllow(t *testing.T) {
	const burst = 50
	l := New(1, burst)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < burst*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := l.Allow("ip:1.1.1.1"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != burst {
		t.Errorf("allowed %d concurrent requests, want exactly burst %d", got, burst)
	}
}

func TestLimiterEvictsIdleKeys(t *testing.T) {
	l := New(10, 5)
	now := time.Now()
	l.now = func() time.Time { return now }
	l.idleFor = time.Second

	if _, ok := l.Allow("old"); !ok {
		t.Fatal("seed request denied")
	}
	now = now.Add(2 * time.Second)
	if _, ok := l.Allow("new"); !ok {
		t.Fatal("follow-up request denied")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.buckets["old"]; ok {
		t.Error("idle key was not evicted")
	}
	if _, ok := l.buckets["new"]; !ok {
		t.Error("fresh key was evicted")
	}
}

func TestLimiterCapsKeyCount(t *testing.T) {
	l := New(10, 5)
	now := time.Now()
	l.now = func() time.Time { return now }
	l.maxKeys = 2
	l.idleFor = time.Hour

	if _, ok := l.Allow("a"); !ok {
		t.Fatal("a denied")
	}
	now = now.Add(time.Millisecond)
	if _, ok := l.Allow("b"); !ok {
		t.Fatal("b denied")
	}
	now = now.Add(time.Millisecond)
	if _, ok := l.Allow("c"); !ok {
		t.Fatal("c denied")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 2 {
		t.Errorf("len=%d, want <= 2", len(l.buckets))
	}
	if _, ok := l.buckets["a"]; ok {
		t.Error("oldest key a should have been evicted at the cap")
	}
}
