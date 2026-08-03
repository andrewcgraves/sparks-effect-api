package routing

import (
	"context"
	"sync"
)

// FakePublisher is an in-memory Publisher for use in tests only. It records
// what was published so a test can assert on the message the API produced, and
// Err makes the unconfirmed-publish path reachable without a broker.
type FakePublisher struct {
	mu sync.Mutex

	// Err is returned instead of publishing, for the failure paths.
	Err error

	// published is read only through Messages, which copies it under the lock.
	// Exporting it would give tests a second, unsynchronised reader — and the
	// suite runs under -race against handlers that publish from request
	// goroutines.
	published []Message
}

func (f *FakePublisher) Publish(_ context.Context, msg Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.published = append(f.published, msg)
	return nil
}

// Messages returns a copy of what has been published so far.
func (f *FakePublisher) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.published...)
}
