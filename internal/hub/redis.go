package hub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// channelName is the one naming scheme both the worker (publisher) and
// the API process (subscriber) use for instrumentID's own candle-change
// channel.
func channelName(instrumentID int64) string {
	return fmt.Sprintf("argus:candles:%d", instrumentID)
}

// Publisher publishes a CandleEvent for instrumentID -- the worker's
// own boundary onto this package, used right after a store write
// succeeds (never before, and never allowed to fail the write itself:
// see the worker's own call site).
type Publisher interface {
	Publish(ctx context.Context, ev CandleEvent) error
}

// RedisPublisher implements Publisher over a real redis.Client.
type RedisPublisher struct {
	client *redis.Client
}

// NewRedisPublisher wires a RedisPublisher over client.
func NewRedisPublisher(client *redis.Client) *RedisPublisher {
	return &RedisPublisher{client: client}
}

// Publish publishes ev on its own instrument's channel.
func (p *RedisPublisher) Publish(ctx context.Context, ev CandleEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("hub: marshal candle event: %w", err)
	}
	if err := p.client.Publish(ctx, channelName(ev.InstrumentID), data).Err(); err != nil {
		return fmt.Errorf("hub: publish candle event instrument=%d: %w", ev.InstrumentID, err)
	}
	return nil
}

// RedisSubscriber implements Subscriber over a real redis.Client, using
// go-redis's own ChannelWithSubscriptions so a resubscribe -- which
// go-redis's PubSub performs transparently after any dropped connection
// (its own doc comment: "PubSub automatically reconnects to Redis
// Server and resubscribes") -- is observable (review item 7: "the hub
// must detect when its Redis subscription drops or reconnects"). By
// the time a *redis.Subscription event for an already-active
// subscription arrives here, the drop has already happened and already
// recovered; that single observation is both signals at once.
type RedisSubscriber struct {
	client *redis.Client
}

// NewRedisSubscriber wires a RedisSubscriber over client.
func NewRedisSubscriber(client *redis.Client) *RedisSubscriber {
	return &RedisSubscriber{client: client}
}

// Subscribe implements Subscriber.
func (s *RedisSubscriber) Subscribe(ctx context.Context, instrumentID int64) (<-chan []byte, <-chan struct{}, func(), error) {
	pubsub := s.client.Subscribe(ctx, channelName(instrumentID))
	// Confirm the initial subscription actually took before handing
	// back a Subscriber a caller might start relying on immediately --
	// mirrors the go-redis docs' own recommended pattern for Subscribe.
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, nil, fmt.Errorf("hub: redis subscribe instrument=%d: %w", instrumentID, err)
	}

	raw := pubsub.ChannelWithSubscriptions()
	events := make(chan []byte, 64)
	reconnected := make(chan struct{}, 1)

	go func() {
		defer close(events)
		seenFirstSubscribe := false
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-raw:
				if !ok {
					return
				}
				switch m := msg.(type) {
				case *redis.Subscription:
					if m.Kind != "subscribe" {
						continue
					}
					if !seenFirstSubscribe {
						seenFirstSubscribe = true
						continue
					}
					select {
					case reconnected <- struct{}{}:
					default:
						// A reconnect notification is already pending;
						// the hub has not drained it yet, and one
						// pending notification already carries "treat
						// every subscription on this instrument as
						// needing a fresh snapshot," so a second one
						// before that is handled adds nothing.
					}
				case *redis.Message:
					select {
					case events <- []byte(m.Payload):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	cancel := func() {
		_ = pubsub.Close()
	}
	return events, reconnected, cancel, nil
}
