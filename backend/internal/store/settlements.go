package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// SettlementLeg is one money movement: a payout to a beneficiary or a refund to
// the buyer. The idempotency key is stable per (pocket, direction, role), so a
// retry reuses the same row and the same gateway key and never moves money
// twice.
type SettlementLeg struct {
	PocketID        string
	Direction       string // "payout" | "refund"
	BeneficiaryRole string // "vendor" | "broker" | "buyer"
	BeneficiaryUser string // optional; empty until a payout account is linked (s8)
	AmountKobo      int64
	IdempotencyKey  string
}

const settlementLegColumns = `
	pocket_id, direction, beneficiary_role,
	COALESCE(beneficiary_user_id::text, ''), amount_kobo, idempotency_key`

// recordSettlementLegsTx persists a pending settlement leg for each money effect
// the transition returned, inside the transition's own transaction. Recording
// the legs atomically with the state change is what makes settlement crash-safe:
// once a pocket is SETTLED or REFUNDED, its legs provably exist and the disburser
// (immediate post-commit, or the sweeper on recovery) pays each exactly once.
// Legs are inert until paid; ON CONFLICT keeps replays idempotent.
func recordSettlementLegsTx(ctx context.Context, tx pgx.Tx, pocketID string, out pocket.Outcome) error {
	for _, e := range out.Effects {
		switch eff := e.(type) {
		case pocket.SchedulePayout:
			for _, leg := range eff.Legs {
				if err := insertSettlementLegTx(ctx, tx, SettlementLeg{
					PocketID:        pocketID,
					Direction:       "payout",
					BeneficiaryRole: string(leg.Role),
					AmountKobo:      leg.AmountKobo,
					IdempotencyKey:  fmt.Sprintf("%s:payout:%s", pocketID, leg.Role),
				}); err != nil {
					return err
				}
			}
		case pocket.ExecuteRefund:
			if err := insertSettlementLegTx(ctx, tx, SettlementLeg{
				PocketID:        pocketID,
				Direction:       "refund",
				BeneficiaryRole: string(eff.BeneficiaryRole),
				AmountKobo:      eff.AmountKobo,
				IdempotencyKey:  fmt.Sprintf("%s:refund:%s", pocketID, eff.BeneficiaryRole),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertSettlementLegTx inserts one pending leg, doing nothing if the leg (by
// idempotency key) already exists.
func insertSettlementLegTx(ctx context.Context, tx pgx.Tx, leg SettlementLeg) error {
	var beneficiary any
	if leg.BeneficiaryUser != "" {
		beneficiary = leg.BeneficiaryUser
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO settlements (pocket_id, direction, beneficiary_role, beneficiary_user_id, amount_kobo, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		leg.PocketID, leg.Direction, leg.BeneficiaryRole, beneficiary, leg.AmountKobo, leg.IdempotencyKey,
	)
	if err != nil {
		return fmt.Errorf("insert settlement leg: %w", err)
	}
	return nil
}

// PendingSettlementLegs returns every unpaid settlement leg across all pockets,
// oldest first. The sweeper drains this to reconcile any leg left pending by a
// crash between commit and disbursement.
func (s *Store) PendingSettlementLegs(ctx context.Context) ([]SettlementLeg, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+settlementLegColumns+` FROM settlements WHERE status = 'pending' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query pending settlements: %w", err)
	}
	defer rows.Close()
	return scanSettlementLegs(rows)
}

// PendingSettlementLegsForPocket returns the unpaid legs of a single pocket, for
// the immediate post-commit disbursement of a just-settled or just-refunded
// pocket.
func (s *Store) PendingSettlementLegsForPocket(ctx context.Context, pocketID string) ([]SettlementLeg, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+settlementLegColumns+` FROM settlements WHERE status = 'pending' AND pocket_id = $1 ORDER BY created_at, id`,
		pocketID)
	if err != nil {
		return nil, fmt.Errorf("query pending settlements for pocket: %w", err)
	}
	defer rows.Close()
	return scanSettlementLegs(rows)
}

func scanSettlementLegs(rows pgx.Rows) ([]SettlementLeg, error) {
	var out []SettlementLeg
	for rows.Next() {
		var leg SettlementLeg
		if err := rows.Scan(&leg.PocketID, &leg.Direction, &leg.BeneficiaryRole,
			&leg.BeneficiaryUser, &leg.AmountKobo, &leg.IdempotencyKey); err != nil {
			return nil, fmt.Errorf("scan settlement leg: %w", err)
		}
		out = append(out, leg)
	}
	return out, rows.Err()
}

// ConfirmSettlement marks a leg confirmed and records its gateway reference. It
// is idempotent: re-confirming an already-confirmed leg is a harmless no-op
// because the disburser only revisits legs still in 'pending'.
func (s *Store) ConfirmSettlement(ctx context.Context, idempotencyKey, gatewayRef string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'confirmed', gateway_ref = $1, updated_at = now()
		 WHERE idempotency_key = $2 AND status = 'pending'`,
		gatewayRef, idempotencyKey)
	if err != nil {
		return fmt.Errorf("confirm settlement: %w", err)
	}
	return nil
}
