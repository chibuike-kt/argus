-- 000015_user_profile_and_onboarding: the fields signup complete and
-- onboarding add to a user (ADR-015 "Signup and onboarding"). All four
-- nullable: an account from before this migration (or, after it, one
-- that has completed signup but not yet finished onboarding) has none
-- of these set, and NULL correctly represents "no record of this" --
-- never backfilled with a fake value.
--
-- display_name: one field, 1-60 characters trimmed, no control
-- characters -- validated in Go (internal/identity), the same "store
-- the real thing, validate once, in one visible place" split
-- email/email_normalized already uses on this table, not a DB CHECK.
--
-- terms_accepted_at/terms_version: copied from signup_verifications
-- (000014) at signup complete, the moment the row this ADR's own
-- fields describe starts to exist.
ALTER TABLE users
    ADD COLUMN display_name           TEXT,
    ADD COLUMN terms_accepted_at      TIMESTAMPTZ,
    ADD COLUMN terms_version          TEXT,
    ADD COLUMN onboarding_completed_at TIMESTAMPTZ;
