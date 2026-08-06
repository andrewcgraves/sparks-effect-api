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

// ErrNotConfirmed reports a publish the broker declined to take responsibility
// for: a nack, or a message accepted onto no queue at all. It is the one
// failure that would otherwise be invisible — the write succeeded locally and
// nothing came back — so it gets a named error rather than a generic one.
var ErrNotConfirmed = errors.New("routing: publish was not confirmed by the broker")

// AMQPPublisher publishes routing jobs to a RabbitMQ queue, in confirm mode.
//
// Confirms are the whole point of this type. Without them a publish is
// fire-and-forget: the API would insert a routing job row, hand the message to
// a socket, and answer 202 whether or not anything ever received it — leaving a
// row stuck in `queued` that a client polls forever and no worker will ever
// see. Publish here does not return nil until the broker has said the message
// is stored.
//
// It connects lazily and reconnects on demand rather than dialling once at
// startup. One mechanism then covers two situations that would otherwise need
// separate handling: a broker that is not up yet when the API boots, and a
// broker that restarts underneath a long-running API.
type AMQPPublisher struct {
	url   string
	queue string
	log   *slog.Logger

	// mu serialises publishes. A single amqp channel is not safe for concurrent
	// use, and deferred confirms are matched to publishes by delivery-tag order,
	// so two requests publishing at once could take each other's confirmation.
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
	// returns carries broker returns for unroutable messages. See Publish for
	// why a confirm alone is not enough.
	returns chan amqp.Return
}

// NewAMQPPublisher builds a publisher for the given broker URL and queue. It
// does not connect; the first Publish does.
func NewAMQPPublisher(url, queue string, log *slog.Logger) *AMQPPublisher {
	return &AMQPPublisher{url: url, queue: queue, log: log}
}

// Publish sends msg and waits for the broker to confirm it.
//
// It publishes to the default exchange with the queue name as the routing key,
// which is why connect declares that queue: the default exchange routes by
// exact queue name, so a declared queue is a guaranteed destination.
//
// Two things have to be true for this to return nil, and a confirm only covers
// the first. A message routed nowhere is *returned* and then acked, so an ack
// on its own would let an unroutable message pass as delivered. The AMQP
// ordering rule is that a return always precedes the ack for the same
// publication, so once the confirm has arrived any return for it is already
// buffered — which is what the non-blocking read below checks. Publishes are
// serialised, so a return found there can only belong to this one.
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

// connect returns a live channel in confirm mode, dialling if there is not one
// already. Callers must hold p.mu.
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

// reset drops the current connection so the next publish dials a fresh one.
// Callers must hold p.mu.
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

// Close releases the broker connection. Safe to call on a publisher that never
// connected.
func (p *AMQPPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}
