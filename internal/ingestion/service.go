package ingestion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// candleStore is the minimal boundary Service needs from
// marketdata.Store. *marketdata.Store satisfies it; tests use a
// handwritten fake so applyCandle's and detectAndRecordGaps' decision
// logic (not-found vs. forming vs. duplicate vs. correction; which
// minutes become a gap) can be verified without a database, the same
// pattern aggregate.go's candleReader already uses (guide 4, 10).
type candleStore interface {
	GetCurrent(ctx context.Context, instrumentID int64, openTime clock.EventTime) (marketdata.Candle, error)
	UpsertForming(ctx context.Context, t marketdata.Tick) (marketdata.Candle, error)
	Close(ctx context.Context, instrumentID int64, openTime clock.EventTime) (marketdata.Candle, error)
	SupersedeCandle(ctx context.Context, instrumentID int64, openTime clock.EventTime, corr marketdata.Correction) (marketdata.Candle, error)
	RecordGap(ctx context.Context, instrumentID int64, start, end clock.EventTime) (marketdata.Gap, error)
	UpsertQuote(ctx context.Context, q marketdata.BidAsk) (marketdata.BidAsk, error)
}

var _ candleStore = (*marketdata.Store)(nil)

// Service drives marketdata.Store from a single Provider's events. It
// owns the two policies the port and Normalizer deliberately left open:
//
//   - What to do with a provider's IsFinal claim (close now vs. cross-
//     check timing): see applyCandle's doc comment.
//   - What to do when Normalizer rejects a value (drop, log, or
//     dead-letter, guide 14): see rejectCandle's doc comment.
//
// It does not register instruments (ADR-006: a symbol existing at a
// provider does not imply enablement, and does not imply registration
// either -- that stays a decision made elsewhere), does not own any
// queue or retry infrastructure (ADR-005 is still open; this stays
// queue-agnostic), and exposes no HTTP surface.
//
// Deferred, known gap: applyCandle has no ordering guard against an
// out-of-order correction. UpsertQuote can tell a stale quote from a
// fresher one because every quote carries QuoteTime and the store
// enforces quote_time ordering (ErrStaleQuote); a candle correction
// carries no equivalent sequence or as-of marker, so if two different
// final values for the same (instrument, open_time) arrive out of
// sequence -- a genuinely newer correction delayed behind a stale
// redelivery, for instance -- applyCandle cannot tell which one is
// actually more authoritative. It calls SupersedeCandle for whichever
// one arrives second, even if that one is the older data. This is not
// fixed in this slice; see applyCandle's doc comment for the detail and
// the WARN log that is this slice's only mitigation (visibility, not
// prevention).
type Service struct {
	providerName string
	provider     Provider
	normalizer   *Normalizer
	instruments  *InstrumentCache
	store        candleStore
	calendar     marketdata.Calendar
	logger       *slog.Logger
}

// NewService wires a Service. providerName identifies provider in the
// instrument registry and in every log line this Service emits; it is
// not necessarily the same string as any Go type name. logger defaults
// to slog.Default() if nil.
func NewService(
	providerName string,
	provider Provider,
	normalizer *Normalizer,
	instrumentCache *InstrumentCache,
	store *marketdata.Store,
	calendar marketdata.Calendar,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		providerName: providerName,
		provider:     provider,
		normalizer:   normalizer,
		instruments:  instrumentCache,
		store:        store,
		calendar:     calendar,
		logger:       logger,
	}
}

// WatchCandles blocks, applying every live candle update for symbol to
// the store, until ctx is canceled or the provider has an unrecoverable
// error. A rejected (malformed, scale-mismatched, or otherwise invalid)
// candle is logged and dropped -- it does not stop the stream; a
// store-level failure does (see handleLiveCandle).
func (s *Service) WatchCandles(ctx context.Context, symbol string) error {
	return s.provider.StreamCandles(ctx, symbol, func(ctx context.Context, c ProviderCandle) error {
		return s.handleLiveCandle(ctx, symbol, c)
	})
}

// WatchQuotes is WatchCandles' counterpart for live quotes.
func (s *Service) WatchQuotes(ctx context.Context, symbol string) error {
	return s.provider.StreamQuotes(ctx, symbol, func(ctx context.Context, q ProviderQuote) error {
		return s.handleLiveQuote(ctx, q)
	})
}

func (s *Service) handleLiveCandle(ctx context.Context, symbol string, raw ProviderCandle) error {
	nc, err := s.normalizer.NormalizeCandle(ctx, s.providerName, raw)
	if err != nil {
		s.rejectCandle(symbol, raw, err)
		return nil
	}
	if err := s.applyCandle(ctx, nc); err != nil {
		return fmt.Errorf("ingestion: apply live candle %s/%s instrument=%d open_time=%s: %w",
			s.providerName, symbol, nc.InstrumentID, nc.OpenTime, err)
	}
	return nil
}

func (s *Service) handleLiveQuote(ctx context.Context, raw ProviderQuote) error {
	nq, err := s.normalizer.NormalizeQuote(ctx, s.providerName, raw)
	if err != nil {
		s.rejectQuote(raw, err)
		return nil
	}
	_, err = s.store.UpsertQuote(ctx, marketdata.BidAsk{
		InstrumentID: nq.InstrumentID,
		Bid:          nq.Bid,
		Ask:          nq.Ask,
		QuoteTime:    nq.QuoteTime,
		IngestedAt:   nq.IngestedAt,
		Provider:     nq.Provider,
	})
	if err != nil {
		if errors.Is(err, marketdata.ErrStaleQuote) {
			// An out-of-order quote is an expected, handled outcome
			// (guide 15: separate expected business outcomes from
			// system failures), not a reason to stop the stream.
			return nil
		}
		return fmt.Errorf("ingestion: upsert live quote %s instrument=%d: %w", s.providerName, nq.InstrumentID, err)
	}
	return nil
}

// IngestHistorical fetches symbol's candles for [start, end) and applies
// each one, then records any missing minute in the range as a gap --
// except a minute the market calendar reports as a scheduled closure,
// which is never a gap (ADR-006).
func (s *Service) IngestHistorical(ctx context.Context, symbol string, start, end time.Time) error {
	facts, err := s.instruments.Get(ctx, s.providerName, symbol)
	if err != nil {
		return fmt.Errorf("ingestion: historical %s/%s: resolve instrument: %w", s.providerName, symbol, err)
	}

	raw, err := s.provider.HistoricalCandles(ctx, symbol, start, end)
	if err != nil {
		return fmt.Errorf("ingestion: historical %s/%s [%s,%s): %w", s.providerName, symbol, start, end, err)
	}

	present := make(map[time.Time]bool, len(raw))
	for _, c := range raw {
		nc, err := s.normalizer.NormalizeCandle(ctx, s.providerName, c)
		if err != nil {
			s.rejectCandle(symbol, c, err)
			continue
		}
		present[nc.OpenTime.Time()] = true
		if err := s.applyCandle(ctx, nc); err != nil {
			return fmt.Errorf("ingestion: apply historical candle %s/%s instrument=%d open_time=%s: %w",
				s.providerName, symbol, nc.InstrumentID, nc.OpenTime, err)
		}
	}

	return s.detectAndRecordGaps(ctx, facts.ID, facts.AssetClass, present, start, end)
}

// applyCandle decides which store operation a normalized candle needs,
// based on the store's current state for its (instrument, open_time) --
// a decision Normalizer cannot make on its own, since it has no view of
// what the store already holds.
//
// This is also where the IsFinal policy this package owns lives: the
// chosen behavior for this slice is to trust the provider's IsFinal
// claim immediately and call Close as soon as it is seen, on either a
// brand-new or a still-forming candle. The alternative -- waiting for
// corroborating evidence before trusting IsFinal, e.g. refusing to close
// a candle until wall-clock time has actually passed its expected close
// boundary, so an early or mistimed "final" message from the provider
// cannot close a minute before it has genuinely elapsed -- is a real
// hardening option this slice does not build. It would need its own
// clock-vs-OpenTime comparison and a grace-period value this package
// does not introduce; flagged for later, not guessed at now.
func (s *Service) applyCandle(ctx context.Context, nc NormalizedCandle) error {
	current, err := s.store.GetCurrent(ctx, nc.InstrumentID, nc.OpenTime)
	switch {
	case errors.Is(err, marketdata.ErrCandleNotFound):
		if _, err := s.store.UpsertForming(ctx, toTick(nc)); err != nil {
			return err
		}
		if nc.IsFinal {
			return s.closeCandle(ctx, nc)
		}
		return nil

	case err != nil:
		return err

	case !current.IsClosed:
		if _, err := s.store.UpsertForming(ctx, toTick(nc)); err != nil {
			return err
		}
		if nc.IsFinal {
			return s.closeCandle(ctx, nc)
		}
		return nil

	case candleValuesEqual(current, nc):
		// Already closed with identical values: a duplicate redelivery,
		// not a correction. At-least-once delivery means this is
		// expected, not an error (guide 14, 16).
		return nil

	default:
		// Already closed with DIFFERENT values: treated as a genuine
		// correction and applied via SupersedeCandle.
		//
		// Known, deliberately deferred gap: there is no ordering check
		// here. nc carries no sequence number or as-of marker this
		// package can compare against current's, so if an incoming
		// "final" message is actually OLDER data than what is already
		// stored -- reordered in transit, a stale retry arriving after
		// a fresher correction already landed -- this branch cannot
		// detect that and will supersede the newer, correct value with
		// the older one. UpsertQuote avoids the equivalent mistake
		// because every quote carries QuoteTime and the store enforces
		// that ordering (ErrStaleQuote); candles have no such field to
		// enforce it with in this slice. The WARN log below is this
		// slice's only mitigation: visibility for an operator to notice
		// a correction that looks wrong, not prevention of applying one.
		// A real fix needs either a provider-supplied revision/sequence
		// marker or an explicit as-of comparison, neither of which this
		// package invents on its own.
		s.logger.Warn("ingestion: superseding closed candle with different values",
			"provider", nc.Provider,
			"instrument_id", nc.InstrumentID,
			"open_time", nc.OpenTime,
			"current_revision", current.Revision,
			"current_open", current.Open, "current_high", current.High, "current_low", current.Low, "current_close", current.Close, "current_volume", current.Volume,
			"incoming_open", nc.Open, "incoming_high", nc.High, "incoming_low", nc.Low, "incoming_close", nc.Close, "incoming_volume", nc.Volume,
		)
		_, err := s.store.SupersedeCandle(ctx, nc.InstrumentID, nc.OpenTime, toCorrection(nc))
		return err
	}
}

func (s *Service) closeCandle(ctx context.Context, nc NormalizedCandle) error {
	if _, err := s.store.Close(ctx, nc.InstrumentID, nc.OpenTime); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// detectAndRecordGaps walks every expected 1-minute slot in [start, end)
// and records consecutive missing, non-scheduled-closure minutes as a
// single gap range. A minute is "missing" when it is both absent from
// present AND the calendar does not affirmatively report it as a
// scheduled closure.
//
// An inconclusive calendar outcome -- the calendar errors (including an
// unimplemented one, e.g. ForexCalendar today) or reports
// MarketStatusUnknown -- is treated as "not a scheduled closure", so the
// minute is still recorded as a gap. Under-reporting a real gap because
// the calendar could not rule it out is worse than a false-positive gap
// that later turns out to have been a scheduled closure once that
// market's calendar is actually implemented.
func (s *Service) detectAndRecordGaps(ctx context.Context, instrumentID int64, assetClass string, present map[time.Time]bool, start, end time.Time) error {
	var gapStart time.Time
	inGap := false

	flush := func(gapEnd time.Time) error {
		if !inGap {
			return nil
		}
		inGap = false
		_, err := s.store.RecordGap(ctx, instrumentID, clock.NewEventTime(gapStart), clock.NewEventTime(gapEnd))
		if err != nil {
			return fmt.Errorf("ingestion: record gap instrument=%d [%s,%s): %w", instrumentID, gapStart, gapEnd, err)
		}
		return nil
	}

	for t := start; t.Before(end); t = t.Add(time.Minute) {
		scheduledClosure := false
		if !present[t] {
			status, calErr := s.calendar.Status(ctx, assetClass, clock.NewEventTime(t))
			scheduledClosure = calErr == nil && status == marketdata.MarketStatusScheduledClosure
		}

		if present[t] || scheduledClosure {
			if err := flush(t); err != nil {
				return err
			}
			continue
		}

		if !inGap {
			gapStart = t
			inGap = true
		}
	}

	return flush(end)
}

// rejectCandle is this package's drop-and-log policy (guide 14) for a
// candle Normalizer rejected: structured-logged, then dropped. It is not
// retried -- a scale mismatch, malformed value, or inconsistent OHLC is
// a permanent-bad-message class, not a transient failure, so retrying
// the identical message would not help. It is not dead-lettered either:
// persisting the rejected raw message for later replay or inspection is
// a real option, deliberately not built without evidence of what
// actually needs it (see Normalizer's package doc for the same call made
// at the validation boundary).
func (s *Service) rejectCandle(symbol string, c ProviderCandle, err error) {
	s.logger.Error("ingestion: rejected candle",
		"provider", s.providerName,
		"symbol", symbol,
		"open_time", c.OpenTime,
		"is_final", c.IsFinal,
		"error", err,
	)
}

func (s *Service) rejectQuote(q ProviderQuote, err error) {
	s.logger.Error("ingestion: rejected quote",
		"provider", s.providerName,
		"symbol", q.Symbol,
		"quote_time", q.QuoteTime,
		"error", err,
	)
}

func toTick(nc NormalizedCandle) marketdata.Tick {
	return marketdata.Tick{
		InstrumentID: nc.InstrumentID,
		OpenTime:     nc.OpenTime,
		Open:         nc.Open,
		High:         nc.High,
		Low:          nc.Low,
		Close:        nc.Close,
		Volume:       nc.Volume,
		TickAligned:  nc.TickAligned,
		Provider:     nc.Provider,
		IngestedAt:   nc.IngestedAt,
	}
}

func toCorrection(nc NormalizedCandle) marketdata.Correction {
	return marketdata.Correction{
		Open:        nc.Open,
		High:        nc.High,
		Low:         nc.Low,
		Close:       nc.Close,
		Volume:      nc.Volume,
		TickAligned: nc.TickAligned,
		Provider:    nc.Provider,
		IngestedAt:  nc.IngestedAt,
	}
}

func candleValuesEqual(current marketdata.Candle, nc NormalizedCandle) bool {
	return current.Open == nc.Open &&
		current.High == nc.High &&
		current.Low == nc.Low &&
		current.Close == nc.Close &&
		current.Volume == nc.Volume
}
