package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrNotConfirmed = errors.New("routing: publish was not confirmed by the broker")

// amqp.Dial's own default is 30s, for the TCP connect and again for the
// handshake. Every caller of connect holds the publisher's lock, so that is
// how long a black-holed broker could keep a publish waiting behind it.
const dialTimeout = 2 * time.Second

type AMQPPublisher struct {
	url   string
	queue string
	log   *slog.Logger
	// A one-slot semaphore rather than a sync.Mutex: Publish can stop waiting
	// for it when its context ends, and Ping can decline to queue for it at all.
	lock    chan struct{}
	up      atomic.Bool
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
}

func NewAMQPPublisher(url, queue string, log *slog.Logger) *AMQPPublisher {
	return &AMQPPublisher{url: url, queue: queue, log: log, lock: make(chan struct{}, 1)}
}

func (p *AMQPPublisher) acquire(ctx context.Context) error {
	select {
	case p.lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("routing: waiting for the broker connection: %w", ctx.Err())
	}
}

func (p *AMQPPublisher) release() { <-p.lock }

func (p *AMQPPublisher) Publish(ctx context.Context, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("routing: marshal message: %w", err)
	}

	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()

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

func (p *AMQPPublisher) Ping(ctx context.Context) error {
	// Dials rather than only inspecting the current channel: the publisher
	// connects lazily, so a fresh process has no connection until its first
	// publish, and one whose connection dropped would otherwise stay "down"
	// until user traffic happened to redial it. A healthy broker answers the
	// check by being reachable.
	//
	// It never queues for the lock. The endpoint is public and unthrottled,
	// so a check that waited would let polling stack up goroutines behind a
	// slow dial, with every Publish stuck at the back of that queue. Someone
	// else holding the lock is either a publish, which settles nothing about
	// the connection, or a dial already under way; report what the last
	// attempt left behind instead.
	select {
	case p.lock <- struct{}{}:
	default:
		if p.up.Load() {
			return nil
		}
		return errors.New("routing: broker check: a connection attempt is already in progress")
	}

	// The dial takes no context, so the caller's deadline is honored here
	// rather than by it. The goroutine outlives an abandoned check by at most
	// dialTimeout per phase, and holds the lock while it does, so there is
	// only ever one.
	done := make(chan error, 1)
	go func() {
		defer p.release()
		_, err := p.connect()
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("routing: broker check: %w", ctx.Err())
	}
}

func (p *AMQPPublisher) connect() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	p.reset()

	conn, err := amqp.DialConfig(p.url, amqp.Config{
		Locale: "en_US",
		Dial:   amqp.DefaultDial(dialTimeout),
	})
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
	p.up.Store(true)
	return ch, nil
}

func (p *AMQPPublisher) reset() {
	p.up.Store(false)
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
	p.lock <- struct{}{}
	defer p.release()
	p.reset()
}
