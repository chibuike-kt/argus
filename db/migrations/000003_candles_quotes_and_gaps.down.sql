DROP TABLE IF EXISTS latest_quotes;
DROP TABLE IF EXISTS candle_gaps;

DROP TRIGGER IF EXISTS trg_candles_enforce_lifecycle ON candles;
DROP FUNCTION IF EXISTS candles_enforce_lifecycle();
DROP TABLE IF EXISTS candles;
