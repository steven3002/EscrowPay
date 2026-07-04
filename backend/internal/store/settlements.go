package store

import (
	"context"
	"fmt"
	"time"

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
	BeneficiaryUser string // optional; empty until a payout account is linked
	AmountKobo      int64
	IdempotencyKey  string
	UpdatedAt       time.Time
}

const settlementLegColumns = `
	pocket_id, direction, beneficiary_role,
	COALESCE(beneficiary_user_id::text, ''), amount_kobo, idempotency_key, updated_at`

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
			&leg.BeneficiaryUser, &leg.AmountKobo, &leg.IdempotencyKey, &leg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan settlement leg: %w", err)
		}
		out = append(out, leg)
	}
	return out, rows.Err()
}

// ClaimSettlementLeg advances one leg from pending to inflight, returning
// whether this caller won the claim. Claiming before the gateway call is what
// bounds disbursement to at most one submission: a leg that is inflight is
// never resubmitted automatically — only a provider notification, a status
// re-query, or an explicit release moves it on.
func (s *Store) ClaimSettlementLeg(ctx context.Context, idempotencyKey string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'inflight', updated_at = now()
		 WHERE idempotency_key = $1 AND status = 'pending'`,
		idempotencyKey)
	if err != nil {
		return false, fmt.Errorf("claim settlement leg: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseSettlementLeg returns an inflight leg to pending for a later retry.
// Callers may only release when the submission definitively did not happen —
// releasing after an ambiguous failure would license a double disbursement.
func (s *Store) ReleaseSettlementLeg(ctx context.Context, idempotencyKey, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'pending', last_error = $1, updated_at = now()
		 WHERE idempotency_key = $2 AND status = 'inflight'`,
		reason, idempotencyKey)
	if err != nil {
		return fmt.Errorf("release settlement leg: %w", err)
	}
	return nil
}

// FailSettlementLeg marks a leg terminally failed with the provider's reason.
// Failed legs are surfaced for operator attention and never retried blindly.
func (s *Store) FailSettlementLeg(ctx context.Context, idempotencyKey, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'failed', last_error = $1, updated_at = now()
		 WHERE idempotency_key = $2 AND status IN ('pending', 'inflight', 'confirmed')`,
		reason, idempotencyKey)
	if err != nil {
		return fmt.Errorf("fail settlement leg: %w", err)
	}
	return nil
}

// InflightSettlementLegs returns every leg awaiting a definitive outcome,
// oldest first, for the reconciliation pass.
func (s *Store) InflightSettlementLegs(ctx context.Context) ([]SettlementLeg, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+settlementLegColumns+` FROM settlements WHERE status = 'inflight' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query inflight settlements: %w", err)
	}
	defer rows.Close()
	return scanSettlementLegs(rows)
}

// ConfirmSettlement marks a leg confirmed and records its gateway reference. It
// is idempotent: re-confirming an already-confirmed leg is a harmless no-op
// because it only advances legs still awaiting an outcome (pending or
// inflight). It doubles as the reconciliation hook for provider payout
// notifications, which may arrive before or after the submitting process
// recorded its own confirmation.
func (s *Store) ConfirmSettlement(ctx context.Context, idempotencyKey, gatewayRef string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE settlements SET status = 'confirmed', gateway_ref = $1, updated_at = now()
		 WHERE idempotency_key = $2 AND status IN ('pending', 'inflight')`,
		gatewayRef, idempotencyKey)
	if err != nil {
		return fmt.Errorf("confirm settlement: %w", err)
	}
	return nil
}
