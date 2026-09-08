package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrNotConfirmed = errors.New("routing: publish was not confirmed by the broker")

type AMQPPublisher struct {
	url     string
	queue   string
	log     *slog.Logger
	mu      sync.Mutex
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
}

func NewAMQPPublisher(url, queue string, log *slog.Logger) *AMQPPublisher {
	return &AMQPPublisher{url: url, queue: queue, log: log}
}

func (p *AMQPPublisher) Publish(ctx context.Context, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("routing: marshal message: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	ch, err := p.connect()
	if err != nil {
		return err
	}

	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", p.queue,
		// mandatory: refuse to let the broker discard a message it cannot route.
		true, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    msg.RoutingJobID,
			Body:         body,
		})
	if err != nil {
		// The channel is likely dead; drop it so the next publish redials
		// rather than reusing a socket that will fail the same way.
		p.reset()
		return fmt.Errorf("routing: publish: %w", err)
	}

	acked, err := confirm.WaitContext(ctx)
	if err != nil {
		p.reset()
		return fmt.Errorf("routing: awaiting publish confirm: %w", err)
	}

	// The return is drained before the ack is judged, and on every path out.
	// Leaving one buffered would hand it to the *next* publish, which would then
	// fail a routing job the broker had actually delivered.
	select {
	case ret := <-p.returns:
		return fmt.Errorf("%w: returned as unroutable (%d %s)", ErrNotConfirmed, ret.ReplyCode, ret.ReplyText)
	default:
	}

	if !acked {
		return fmt.Errorf("%w: nacked", ErrNotConfirmed)
	}

	p.log.Debug("routing: published job", "routing_job_id", msg.RoutingJobID,
		"trace_id", msg.TraceID, "queue", p.queue)
	return nil
}

func (p *AMQPPublisher) connect() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	p.reset()

	conn, err := amqp.Dial(p.url)
	if err != nil {
		return nil, fmt.Errorf("routing: dial broker: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("routing: open channel: %w", err)
	}
	// Declared durable so the queue — and the jobs waiting in it — survive a
	// broker restart, matching the persistent delivery mode above. The worker
	// declares the same queue with the same arguments; a mismatch would be
	// rejected by the broker rather than silently creating a second one.
	if _, err := ch.QueueDeclare(p.queue, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("routing: declare queue %q: %w", p.queue, err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("routing: enable publisher confirms: %w", err)
	}

	p.conn, p.ch = conn, ch
	// Buffered so a return never blocks the broker's reader goroutine in the
	// window before Publish drains it.
	p.returns = ch.NotifyReturn(make(chan amqp.Return, 1))
	return ch, nil
}

func (p *AMQPPublisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	p.returns = nil
}

func (p *AMQPPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}
