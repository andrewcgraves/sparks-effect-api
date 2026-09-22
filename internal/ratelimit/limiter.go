package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const defaultMaxKeys = 10_000

const defaultIdleFor = 2 * time.Minute

type Limiter struct {
	limit   rate.Limit
	burst   int
	mu      sync.Mutex
	buckets map[string]*tracked
	idleFor time.Duration
	maxKeys int
	now     func() time.Time
}

type tracked struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func New(tokensPerMin, burst int) *Limiter {
	if tokensPerMin <= 0 || burst <= 0 {
		return nil
	}
	return &Limiter{
		limit:   rate.Limit(float64(tokensPerMin) / 60.0),
		burst:   burst,
		buckets: make(map[string]*tracked),
		// Policies are per-minute; twice that window is long enough to drop
		// idle keys without resetting a caller who is still sending traffic.
		idleFor: defaultIdleFor,
		maxKeys: defaultMaxKeys,
		now:     time.Now,
	}
}

func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	if l == nil {
		return 0, true
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.evictIdle(now)
	b := l.bucket(key, now)
	b.lastSeen = now

	res := b.lim.ReserveN(now, 1)
	if delay := res.DelayFrom(now); delay > 0 {
		// ReserveN consumed a future token; give it back so a denied
		// request does not push the next legitimate one further out.
		res.CancelAt(now)
		if delay < time.Second {
			delay = time.Second
		}
		return delay, false
	}
	return 0, true
}

func (l *Limiter) bucket(key string, now time.Time) *tracked {
	if b, ok := l.buckets[key]; ok {
		return b
	}
	if len(l.buckets) >= l.maxKeys {
		l.evictOldest()
	}
	b := &tracked{lim: rate.NewLimiter(l.limit, l.burst), lastSeen: now}
	l.buckets[key] = b
	return b
}

func (l *Limiter) evictIdle(now time.Time) {
	cutoff := now.Add(-l.idleFor)
	for k, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

func (l *Limiter) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, b := range l.buckets {
		if first || b.lastSeen.Before(oldestTime) {
			oldestKey = k
			oldestTime = b.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
