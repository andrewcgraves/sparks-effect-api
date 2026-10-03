package routing_test

import (
	"context"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
)

// A broker that accepts the TCP connection and then says nothing: the case a
// dial can only escape by timing out.
func blackholeBroker(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = ln.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, c := range held {
				_ = c.Close()
			}
		})
	}
	t.Cleanup(stop)
	return "amqp://guest:guest@" + ln.Addr().String() + "/", stop
}

func TestPingAgainstASilentBrokerNeitherPilesUpNorStarvesPublish(t *testing.T) {
	url, stop := blackholeBroker(t)
	pub := routing.NewAMQPPublisher(url, "unused", logger.Discard())
	// Close waits on the publisher lock. The abandoned dial holds that lock
	// until the handshake deadline (dialTimeout), so closing the socket first
	// lets the dial return instead of cleanup waiting the deadline out.
	defer pub.Close()
	defer stop()

	baseline := runtime.NumGoroutine()
	for i := range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		err := pub.Ping(ctx)
		cancel()
		if err == nil {
			t.Fatalf("ping %d: reported a silent broker as up", i)
		}
		if waited := time.Since(start); waited > 500*time.Millisecond {
			t.Fatalf("ping %d: took %s against a 50ms deadline", i, waited)
		}
	}
	// One dial in flight, plus whatever the client library runs beside it.
	// Queued checks would show up here as one goroutine per ping.
	if grown := runtime.NumGoroutine() - baseline; grown > 5 {
		t.Errorf("goroutines grew by %d over 20 pings; checks are queuing", grown)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := pub.Publish(ctx, routing.Message{RoutingJobID: "blackhole"}); err == nil {
		t.Fatal("Publish reported success against a silent broker")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("Publish with a 300ms deadline returned after %s", waited)
	}
}
