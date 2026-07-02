package store

import (
	"context"
	"fmt"
)

// SettlementLeg is one money movement the effects executor records before
// calling the gateway. The idempotency key is stable per (pocket, direction,
// role), so retries reuse the same row and the same gateway key.
type SettlementLeg struct {
	PocketID        string
	Direction       string // "payout" | "refund"
	BeneficiaryRole string // "vendor" | "broker" | "buyer"
	BeneficiaryUser string // optional; empty until a payout account is linked (s8)
	AmountKobo      int64
	IdempotencyKey  string
}

// RecordSettlementLeg inserts a pending settlement leg, or does nothing if the
// leg already exists. It returns whether a new row was created, letting the
// caller skip a duplicate gateway call on replay.
func (s *Store) RecordSettlementLeg(ctx context.Context, leg SettlementLeg) (created bool, err error) {
	var beneficiary any
	if leg.BeneficiaryUser != "" {
		beneficiary = leg.BeneficiaryUser
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO settlements (pocket_id, direction, beneficiary_role, beneficiary_user_id, amount_kobo, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		leg.PocketID, leg.Direction, leg.BeneficiaryRole, beneficiary, leg.AmountKobo, leg.IdempotencyKey,
	)
	if err != nil {
		return false, fmt.Errorf("record settlement leg: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ConfirmSettlement marks a leg confirmed and records its gateway reference.
func (s *Store) ConfirmSettlement(ctx context.Context, idempotencyKey, gatewayRef string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'confirmed', gateway_ref = $1, updated_at = now() WHERE idempotency_key = $2`,
		gatewayRef, idempotencyKey)
	if err != nil {
		return fmt.Errorf("confirm settlement: %w", err)
	}
	return nil
}
