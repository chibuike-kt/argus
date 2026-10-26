-- 000003_candles_quotes_and_gaps: canonical 1-minute candle store with
-- revisions, gap tracking, and the latest-quote store (ADR-006, ADR-011,
-- ADR-019). Derived intervals (5m/15m/1h/4h/1d) are computed on read from
-- this table; they are not materialized here.

-- candles: the canonical 1-minute OHLCV series.
--
-- Lifecycle (approved fix: provisional candles do not create revisions):
--   - Forming:    revision = 1, is_closed = false, superseded_at IS NULL.
--                 A single overwritable row. Incoming ticks update this
--                 row's OHLCV in place; nothing about it is frozen yet.
--   - Closed:     is_closed = true, superseded_at IS NULL. The candle
--                 froze on close. This is the current, authoritative
--                 value for this open_time.
--   - Superseded: superseded_at IS NOT NULL, superseded_by points at the
--                 correcting row. Terminal: no further changes of any
--                 kind. Only a correction to an already-CLOSED candle
--                 creates revision+1 -- a provisional tick never does.
--
-- All of the above is enforced by trg_candles_enforce_lifecycle, not left
-- as a convention: see candles_enforce_lifecycle() below.
CREATE TABLE candles (
    id              BIGSERIAL   PRIMARY KEY,
    instrument_id   BIGINT      NOT NULL REFERENCES instruments(id),
    open_time       TIMESTAMPTZ NOT NULL, -- EventTime: bar open, UTC, minute-aligned
    close_time      TIMESTAMPTZ NOT NULL, -- EventTime: bar close, always open_time + 1 minute
    open            BIGINT      NOT NULL, -- quant.Price scaled by instruments.price_scale
    high            BIGINT      NOT NULL,
    low             BIGINT      NOT NULL,
    close           BIGINT      NOT NULL,
    volume          BIGINT      NOT NULL, -- quant.Qty scaled by instruments.qty_scale
    revision        INTEGER     NOT NULL DEFAULT 1,
    is_closed       BOOLEAN     NOT NULL, -- false = forming (provisional), true = frozen
    tick_aligned    BOOLEAN     NOT NULL, -- data-quality signal: OHLC values land on tick_size
    provider        TEXT        NOT NULL,
    ingested_at     TIMESTAMPTZ NOT NULL, -- IngestionTime; see note on open semantics below
    superseded_at   TIMESTAMPTZ,          -- set only when a correction replaces this closed candle
    superseded_by   BIGINT      REFERENCES candles(id),

    CONSTRAINT candles_ohlc_nonnegative CHECK (open >= 0 AND high >= 0 AND low >= 0 AND close >= 0),
    CONSTRAINT candles_ohlc_consistent CHECK (
        high >= low AND high >= open AND high >= close AND low <= open AND low <= close
    ),
    CONSTRAINT candles_volume_nonnegative CHECK (volume >= 0),
    CONSTRAINT candles_revision_positive CHECK (revision >= 1),
    -- The canonical series is strictly 1-minute bars; derived intervals are
    -- computed on read elsewhere, never stored in this table.
    CONSTRAINT candles_duration_one_minute CHECK (close_time = open_time + INTERVAL '1 minute'),
    -- Deterministic regardless of session timezone: converts to a naive
    -- UTC wall-clock reading, truncates, and converts back, rather than
    -- relying on date_trunc's session-timezone-dependent behavior on
    -- timestamptz (guide 13: UTC internally, explicit serialization rules).
    CONSTRAINT candles_open_time_minute_aligned CHECK (
        (date_trunc('minute', open_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') = open_time
    ),
    -- superseded_by implies superseded_at, but not the reverse: see the
    -- two-step correction handshake in candles_enforce_lifecycle() below.
    -- (superseded_at, superseded_by) = (NULL, NULL) is current/forming,
    -- (NOT NULL, NULL) is "being superseded" (mid-correction), and
    -- (NOT NULL, NOT NULL) is fully resolved and terminal.
    CONSTRAINT candles_superseded_consistency CHECK (
        superseded_by IS NULL OR superseded_at IS NOT NULL
    ),
    -- Full candle identity: one row per instrument, open_time and revision.
    UNIQUE (instrument_id, open_time, revision)
);

-- Exactly one current (non-superseded) row per (instrument_id, open_time)
-- is a database guarantee, not an application convention: a second INSERT
-- racing to create a new "current" row for the same open_time collides on
-- this index rather than silently producing two current revisions.
CREATE UNIQUE INDEX idx_candles_current ON candles (instrument_id, open_time)
    WHERE superseded_at IS NULL;

-- Note: point-in-time replay ("what was known as of ingested_at <= T, take
-- the max revision per open_time at that time") does not use the index
-- above and needs its own; not added yet (flagged, not forgotten).

-- Correcting a closed candle is a two-step handshake across the
-- correction's transaction, not a single update, because Postgres cannot
-- make a partial unique index DEFERRABLE: the old row must leave the
-- "current" index (superseded_at set) before the new revision can be
-- inserted without colliding on it, but superseded_by can only name the
-- new row once that insert has happened and returned its id. So:
--   1. UPDATE the old row: set superseded_at (superseded_by stays NULL).
--      This removes it from idx_candles_current.
--   2. INSERT the correcting row at revision+1 (now unique index-safe).
--   3. UPDATE the old row again: set superseded_by to the new row's id.
-- All three must happen in one transaction; the function below is what
-- enforces that only exactly this sequence of changes is legal.
CREATE OR REPLACE FUNCTION candles_enforce_lifecycle() RETURNS TRIGGER AS $$
BEGIN
    -- Fully resolved supersession is terminal: no further changes.
    IF OLD.superseded_at IS NOT NULL AND OLD.superseded_by IS NOT NULL THEN
        RAISE EXCEPTION 'candle % is superseded and immutable', OLD.id;
    END IF;

    -- Identity and revision never change in place. A correction inserts a
    -- new row at revision+1 and supersedes this one; it does not rewrite
    -- this row's identity or revision.
    IF NEW.instrument_id IS DISTINCT FROM OLD.instrument_id
       OR NEW.open_time IS DISTINCT FROM OLD.open_time
       OR NEW.revision IS DISTINCT FROM OLD.revision THEN
        RAISE EXCEPTION 'candle % identity (instrument_id, open_time, revision) is immutable', OLD.id;
    END IF;

    IF NOT OLD.is_closed THEN
        -- Forming: OHLCV/tick_aligned/provider/ingested_at may be
        -- overwritten in place by later ticks, and is_closed may freeze
        -- (false -> true), but a forming candle must never be marked
        -- superseded -- corrections apply only to closed candles.
        IF NEW.superseded_at IS NOT NULL OR NEW.superseded_by IS NOT NULL THEN
            RAISE EXCEPTION 'candle % is still forming and cannot be superseded', OLD.id;
        END IF;
        RETURN NEW;
    END IF;

    -- OLD.is_closed is true from here on: OHLCV, tick_aligned, provider,
    -- ingested_at and is_closed itself are frozen regardless of which
    -- handshake step this update represents.
    IF NEW.open IS DISTINCT FROM OLD.open
       OR NEW.high IS DISTINCT FROM OLD.high
       OR NEW.low IS DISTINCT FROM OLD.low
       OR NEW.close IS DISTINCT FROM OLD.close
       OR NEW.volume IS DISTINCT FROM OLD.volume
       OR NEW.tick_aligned IS DISTINCT FROM OLD.tick_aligned
       OR NEW.provider IS DISTINCT FROM OLD.provider
       OR NEW.ingested_at IS DISTINCT FROM OLD.ingested_at
       OR NEW.is_closed IS DISTINCT FROM OLD.is_closed THEN
        RAISE EXCEPTION 'candle % is closed: OHLCV and is_closed are frozen; insert a new revision to correct it', OLD.id;
    END IF;

    IF OLD.superseded_at IS NULL THEN
        -- Handshake step 1: begin superseding. superseded_by must still
        -- be NULL -- it cannot legally be known yet.
        IF NEW.superseded_at IS NULL OR NEW.superseded_by IS NOT NULL THEN
            RAISE EXCEPTION 'candle % is closed: the only legal update is setting superseded_at to begin superseding it', OLD.id;
        END IF;
    ELSE
        -- Handshake step 2: resolve the pointer. superseded_at must not
        -- change again.
        IF NEW.superseded_at IS DISTINCT FROM OLD.superseded_at OR NEW.superseded_by IS NULL THEN
            RAISE EXCEPTION 'candle % is being superseded: the only legal update is setting superseded_by', OLD.id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_candles_enforce_lifecycle
    BEFORE UPDATE ON candles
    FOR EACH ROW
    EXECUTE FUNCTION candles_enforce_lifecycle();

-- candle_gaps: missing canonical minutes. Reconnection alone never resolves
-- a gap; resolved_at is set only once backfill or the provider confirms
-- what actually happened (ADR-006).
CREATE TABLE candle_gaps (
    id              BIGSERIAL   PRIMARY KEY,
    instrument_id   BIGINT      NOT NULL REFERENCES instruments(id),
    gap_start       TIMESTAMPTZ NOT NULL,
    gap_end         TIMESTAMPTZ NOT NULL,
    detected_at     TIMESTAMPTZ NOT NULL,
    resolved_at     TIMESTAMPTZ, -- NULL = still open
    resolution      TEXT,        -- e.g. 'backfilled', 'provider_confirmed_no_data'; free text for now

    CONSTRAINT candle_gaps_range CHECK (gap_start < gap_end),
    -- A gap cannot be marked resolved without saying how.
    CONSTRAINT candle_gaps_resolution_requires_resolved_at CHECK (
        resolved_at IS NULL OR resolution IS NOT NULL
    ),
    UNIQUE (instrument_id, gap_start, gap_end)
);

-- latest_quotes: one row per instrument, latest bid/ask only (ADR-006;
-- historical quote retention and tick-level replay are not a confirmed
-- requirement). Crossed quotes (ask < bid) can occur transiently in real
-- markets and are not rejected here -- that is a data-quality signal for
-- the application layer, not a schema-level impossibility.
CREATE TABLE latest_quotes (
    instrument_id   BIGINT PRIMARY KEY REFERENCES instruments(id),
    bid             BIGINT      NOT NULL, -- quant.Price scaled by instruments.price_scale
    ask             BIGINT      NOT NULL,
    quote_time      TIMESTAMPTZ NOT NULL, -- EventTime
    ingested_at     TIMESTAMPTZ NOT NULL, -- IngestionTime
    provider        TEXT        NOT NULL,

    CONSTRAINT latest_quotes_nonnegative CHECK (bid >= 0 AND ask >= 0)
);
