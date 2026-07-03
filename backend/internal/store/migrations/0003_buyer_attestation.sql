-- +goose Up

-- Records the buyer's non-receipt attestation while a pocket is FROZEN. It is a
-- precondition for the grace-lapse refund (transition #9): the sweeper refunds a
-- frozen pocket only when the buyer has attested, never on a timer alone. This
-- upholds the invariant that no refund is automatic.
ALTER TABLE pockets ADD COLUMN buyer_nonreceipt_attested_at timestamptz;

-- +goose Down

ALTER TABLE pockets DROP COLUMN buyer_nonreceipt_attested_at;
