-- +goose Up

-- Settlement legs gain an explicit in-flight claim state. A leg is claimed
-- (pending → inflight) before its gateway call and confirmed after, so a crash
-- between call and confirmation can never lead to a blind second submission:
-- inflight legs are only resolved by a provider notification, a status
-- re-query, or operator action — never by an automatic retry.
ALTER TABLE settlements DROP CONSTRAINT settlements_status_check;
ALTER TABLE settlements ADD CONSTRAINT settlements_status_check
    CHECK (status IN ('pending', 'inflight', 'confirmed', 'failed'));

-- Diagnostic trail for legs that were rejected or released back for retry.
ALTER TABLE settlements ADD COLUMN last_error text NOT NULL DEFAULT '';

-- The provider transaction id of the payment that funded the pocket, captured
-- from the funding notification. It is the key a payment-rail refund of the
-- original charge would be issued against.
ALTER TABLE pockets ADD COLUMN funding_transaction_id text;

-- +goose Down

ALTER TABLE pockets DROP COLUMN funding_transaction_id;
ALTER TABLE settlements DROP COLUMN last_error;
ALTER TABLE settlements DROP CONSTRAINT settlements_status_check;
ALTER TABLE settlements ADD CONSTRAINT settlements_status_check
    CHECK (status IN ('pending', 'confirmed', 'failed'));
