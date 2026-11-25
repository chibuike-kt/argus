-- The per timeframe documents in user_drawings were never touched, so
-- dropping the instrument documents loses only edits made after the
-- upgrade.
DROP TABLE IF EXISTS user_instrument_drawings;
