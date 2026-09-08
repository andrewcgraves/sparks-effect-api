package routing

import (
	"context"
	"sync"
)

type FakePublisher struct {
	mu        sync.Mutex
	Err       error
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

func (f *FakePublisher) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.published...)
}
