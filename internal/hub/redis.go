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

// seqKey is the Redis key holding instrumentID's own monotonic publish
// counter -- separate from the pub/sub channel itself, since INCR and
// PUBLISH are two different Redis operations.
func seqKey(instrumentID int64) string {
	return fmt.Sprintf("argus:candles:seq:%d", instrumentID)
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

// Publish assigns ev its own instrument's next sequence number (Redis
// INCR, starting at 1) and publishes it on that instrument's channel.
// INCR and PUBLISH are two separate round trips, not one atomic
// operation: if the process dies or the connection drops between them,
// the sequence number is consumed but the event never arrives, which
// looks -- to the hub -- identical to the event having been lost in
// transit. That is the point, not a gap in the design: every kind of
// loss this system needs to recover from (a dropped Redis connection,
// a message lost in transit, a publish that never completes) reduces
// to the same observable symptom, a sequence gap, and the same
// recovery (review item 7, revised -- see types.go's own CandleEvent.Seq).
func (p *RedisPublisher) Publish(ctx context.Context, ev CandleEvent) error {
	seq, err := p.client.Incr(ctx, seqKey(ev.InstrumentID)).Result()
	if err != nil {
		return fmt.Errorf("hub: incr seq instrument=%d: %w", ev.InstrumentID, err)
	}
	if seq < 0 {
		// INCR on a key it itself created only ever counts up from 1;
		// a negative result means something else wrote a negative
		// value into this key (an operational mistake, not something
		// to guess past by wrapping it into a huge uint64 instead).
		return fmt.Errorf("hub: seq key for instrument=%d holds a negative value (%d)", ev.InstrumentID, seq)
	}
	ev.Seq = uint64(seq)

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("hub: marshal candle event: %w", err)
	}
	if err := p.client.Publish(ctx, channelName(ev.InstrumentID), data).Err(); err != nil {
		return fmt.Errorf("hub: publish candle event instrument=%d: %w", ev.InstrumentID, err)
	}
	return nil
}

// RedisSubscriber implements Subscriber over a real redis.Client.
// Deliberately simple: it hands back raw message payloads and nothing
// else. An earlier version tried to detect a dropped-and-recovered
// connection via go-redis's own ChannelWithSubscriptions resubscribe
// notifications; a real repro (CLIENT KILL TYPE pubsub against a
// connected, subscribed client) showed that signal never arrives in a
// way this package could rely on -- delivery just silently resumed,
// with no resubscribe event observed at all. Recovery now depends on
// nothing from this type: it is driven entirely by CandleEvent.Seq,
// which this type does not even look at.
type RedisSubscriber struct {
	client *redis.Client
}

// NewRedisSubscriber wires a RedisSubscriber over client.
func NewRedisSubscriber(client *redis.Client) *RedisSubscriber {
	return &RedisSubscriber{client: client}
}

// Subscribe implements Subscriber.
func (s *RedisSubscriber) Subscribe(ctx context.Context, instrumentID int64) (<-chan []byte, func(), error) {
	pubsub := s.client.Subscribe(ctx, channelName(instrumentID))
	// Confirm the initial subscription actually took before handing
	// back a Subscriber a caller might start relying on immediately --
	// mirrors the go-redis docs' own recommended pattern for Subscribe.
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, nil, fmt.Errorf("hub: redis subscribe instrument=%d: %w", instrumentID, err)
	}

	raw := pubsub.Channel()
	events := make(chan []byte, 64)

	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-raw:
				if !ok {
					return
				}
				select {
				case events <- []byte(msg.Payload):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	cancel := func() {
		_ = pubsub.Close()
	}
	return events, cancel, nil
}
