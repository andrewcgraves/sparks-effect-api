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
func blackholeBroker(t *testing.T) string {
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
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	return "amqp://guest:guest@" + ln.Addr().String() + "/"
}

func TestPingAgainstASilentBrokerNeitherPilesUpNorStarvesPublish(t *testing.T) {
	pub := routing.NewAMQPPublisher(blackholeBroker(t), "unused", logger.Discard())
	defer pub.Close()

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
