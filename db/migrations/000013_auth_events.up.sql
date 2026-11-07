-- 000013_auth_events: an append-only audit log of authentication
-- events (ADR-015 addendum row 12). user_id is nullable: a failed
-- login against an email that matches no account still gets a row (the
-- whole point of auditing abuse), but there is no user to reference.
-- detail is JSONB for whatever a specific kind wants to record, with
-- one absolute rule enforced only in Go, never in SQL (there is no
-- column-level way to check "not a secret"): no password, token,
-- TOTP secret or code ever goes in it (CLAUDE.md rule 3 / this brief's
-- own rule 3).
--
-- Append-only like setup_events (000008), but stricter: that trigger
-- only blocks UPDATE (nothing in this codebase ever deletes a setup
-- event either, but the brief for this table asks for defense against
-- both statements explicitly, since an audit log that can be deleted
-- is not one a security review can rely on).
CREATE TABLE auth_events (
    id          BIGSERIAL   PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id     BIGINT      REFERENCES users(id),
    kind        TEXT        NOT NULL,
    ip          TEXT,
    user_agent  TEXT,
    detail      JSONB       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX idx_auth_events_user_id ON auth_events (user_id);
CREATE INDEX idx_auth_events_at ON auth_events (at);

CREATE OR REPLACE FUNCTION auth_events_prevent_update_or_delete() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'auth event % is immutable once inserted; nothing in this table is ever corrected or removed', OLD.id;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_auth_events_prevent_update_or_delete
    BEFORE UPDATE OR DELETE ON auth_events
    FOR EACH ROW
    EXECUTE FUNCTION auth_events_prevent_update_or_delete();
