-- +goose Up

-- The delivery clock starts at funding (transition #2), so the funding
-- transition needs the window duration to compute delivery_deadline. Persist it
-- alongside inspection_window_minutes; both are stored as integer minutes.
ALTER TABLE pockets ADD COLUMN delivery_window_minutes integer NOT NULL DEFAULT 0 CHECK (delivery_window_minutes >= 0);

-- Funding-link artifact minted by the gateway when terms are accepted
-- (transition #1). Kept for audit and to surface a pay affordance; the real
-- adapter stores its virtual-account reference here in the payment scope.
ALTER TABLE pockets ADD COLUMN funding_link_ref text;
ALTER TABLE pockets ADD COLUMN funding_link_url text;

-- Buyer-retrievable copy of the Release Code, encrypted at rest under a
-- server-held key (AES-256-GCM). release_code_hash remains the HMAC verifier
-- for code entry; this column exists only so the buyer-only endpoint can reveal
-- the plaintext on demand. A database-only compromise reveals neither, because
-- both keys live outside the database.
ALTER TABLE pockets ADD COLUMN release_code_enc text;

-- +goose Down

ALTER TABLE pockets DROP COLUMN release_code_enc;
ALTER TABLE pockets DROP COLUMN funding_link_url;
ALTER TABLE pockets DROP COLUMN funding_link_ref;
ALTER TABLE pockets DROP COLUMN delivery_window_minutes;
