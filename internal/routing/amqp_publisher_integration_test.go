package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	amqp "github.com/rabbitmq/amqp091-go"
)

func brokerURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		url = os.Getenv("AMQP_URL")
	}
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_AMQP_URL (or AMQP_URL) must be set for queue integration tests in CI")
		}
		t.Skip("set TEST_AMQP_URL to run queue integration tests (see `make mq-up`)")
	}
	return url
}

func testQueue(t *testing.T, url string) string {
	t.Helper()
	name := "test-routing-" + t.Name()

	t.Cleanup(func() {
		conn, err := amqp.Dial(url)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		ch, err := conn.Channel()
		if err != nil {
			return
		}
		defer func() { _ = ch.Close() }()
		_, _ = ch.QueueDelete(name, false, false, false)
	})
	return name
}

func consumeOne(t *testing.T, url, queue string) []byte {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()

	deliveries, err := ch.Consume(queue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	select {
	case d := <-deliveries:
		return d.Body
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived on the queue within 5s")
		return nil
	}
}

func TestIntegration_PublishedMessageMatchesTheFixture(t *testing.T) {
	url := brokerURL(t)
	queue := testQueue(t, url)

	pub := routing.NewAMQPPublisher(url, queue, logger.Discard())
	defer pub.Close()

	if err := pub.Publish(context.Background(), goldenMessage()); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var got routing.Message
	if err := json.Unmarshal(consumeOne(t, url, queue), &got); err != nil {
		t.Fatalf("unmarshal delivered body: %v", err)
	}

	want, err := json.Marshal(goldenMessage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(gotJSON) != string(want) {
		t.Errorf("delivered message differs from what was published.\ngot  %s\nwant %s", gotJSON, want)
	}
}

func TestIntegration_PublishFailsWhenTheBrokerIsUnreachable(t *testing.T) {
	// A port nothing is listening on, so the dial itself fails.
	pub := routing.NewAMQPPublisher("amqp://guest:guest@127.0.0.1:1/", "unused", logger.Discard())
	defer pub.Close()

	err := pub.Publish(context.Background(), goldenMessage())
	if err == nil {
		t.Fatal("Publish reported success with no broker to publish to")
	}
}

func TestIntegration_PublisherReconnectsAfterItsConnectionDrops(t *testing.T) {
	url := brokerURL(t)
	queue := testQueue(t, url)

	pub := routing.NewAMQPPublisher(url, queue, logger.Discard())
	defer pub.Close()

	if err := pub.Publish(context.Background(), goldenMessage()); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	// Close drops the underlying connection exactly as a broker restart would,
	// leaving the publisher holding a dead channel.
	pub.Close()

	if err := pub.Publish(context.Background(), goldenMessage()); err != nil {
		t.Fatalf("Publish after the connection dropped: %v", err)
	}

	if body := consumeOne(t, url, queue); len(body) == 0 {
		t.Error("no message on the queue after reconnecting")
	}
}

func TestIntegration_UnroutableMessageIsNotTreatedAsConfirmed(t *testing.T) {
	url := brokerURL(t)

	// Publishing to the default exchange routes by exact queue name. Naming a
	// queue that was never declared is therefore a message with no destination.
	pub := routing.NewAMQPPublisher(url, "declared-queue-"+t.Name(), logger.Discard())
	defer pub.Close()
	t.Cleanup(func() {
		conn, err := amqp.Dial(url)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		ch, err := conn.Channel()
		if err != nil {
			return
		}
		defer func() { _ = ch.Close() }()
		_, _ = ch.QueueDelete("declared-queue-"+t.Name(), false, false, false)
	})

	// The publisher declares its own queue, so its own publishes are always
	// routable. Reach past it to publish at a name nothing declared.
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()
	if err := ch.Confirm(false); err != nil {
		t.Fatalf("confirm mode: %v", err)
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))

	confirm, err := ch.PublishWithDeferredConfirmWithContext(context.Background(),
		"", "no-such-queue-"+t.Name(), true, false,
		amqp.Publishing{ContentType: "application/json", Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	acked, err := confirm.WaitContext(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	// This is the premise the publisher's return check rests on: the broker
	// acks an unroutable message, having already returned it.
	if !acked {
		t.Skip("broker nacked an unroutable message; the return check is redundant here")
	}
	select {
	case <-returns:
	case <-time.After(2 * time.Second):
		t.Error("broker acked an unroutable message and never returned it; " +
			"AMQPPublisher.Publish would report success for a message that went nowhere")
	}
}

func TestErrNotConfirmed_isMatchableThroughWrapping(t *testing.T) {
	wrapped := errors.New("outer: " + routing.ErrNotConfirmed.Error())
	if errors.Is(wrapped, routing.ErrNotConfirmed) {
		t.Fatal("a merely similar error matched; the test below would prove nothing")
	}
	if !errors.Is(errors.Join(routing.ErrNotConfirmed, errors.New("nacked")), routing.ErrNotConfirmed) {
		t.Error("ErrNotConfirmed is not matchable when wrapped")
	}
}
