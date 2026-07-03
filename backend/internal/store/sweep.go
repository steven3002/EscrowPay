package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// The sweep methods drive the clock-triggered transitions. Each claims at most
// one due pocket with FOR UPDATE SKIP LOCKED — the canonical work-queue lock, so
// concurrent sweepers and API writers never contend — then runs it through the
// same single write path as every other transition (guard, state write, one
// event, settlement legs). The bool return reports whether a pocket was claimed,
// letting the caller drain a rule until nothing is due.

// SweepOneExpiredFunding advances one CREATED pocket whose funding window has
// lapsed to EXPIRED (transition #3).
func (s *Store) SweepOneExpiredFunding(ctx context.Context, now time.Time) (WriteResult, bool, error) {
	return s.sweepOne(ctx, now, "system",
		`state = $1 AND funding_expires_at IS NOT NULL AND funding_expires_at <= $2`,
		[]any{string(pocket.StateCreated), now},
		pocket.Event{Kind: pocket.EvFundingExpired})
}

// SweepOneDeliveryDeadline freezes one FUNDED pocket whose delivery deadline has
// lapsed without a Release Code (transition #7).
func (s *Store) SweepOneDeliveryDeadline(ctx context.Context, now time.Time) (WriteResult, bool, error) {
	return s.sweepOne(ctx, now, "system",
		`state = $1 AND delivery_deadline IS NOT NULL AND delivery_deadline <= $2`,
		[]any{string(pocket.StateFunded), now},
		pocket.Event{Kind: pocket.EvDeliveryDeadlineLapsed})
}

// SweepOneGraceRefund refunds one FROZEN pocket whose grace period has lapsed
// with the buyer's non-receipt attestation on record (transition #9). Absent the
// attestation the pocket is left frozen: there is no refund on a timer alone.
func (s *Store) SweepOneGraceRefund(ctx context.Context, now time.Time) (WriteResult, bool, error) {
	return s.sweepOne(ctx, now, "system",
		`state = $1 AND grace_deadline IS NOT NULL AND grace_deadline <= $2
		 AND buyer_nonreceipt_attested_at IS NOT NULL`,
		[]any{string(pocket.StateFrozen), now},
		pocket.Event{Kind: pocket.EvRefundFrozen, Reason: "grace_nonreceipt"})
}

// SweepOneDueSettlement settles one DELIVERED_PENDING pocket whose inspection
// window has elapsed (transition #11).
func (s *Store) SweepOneDueSettlement(ctx context.Context, now time.Time) (WriteResult, bool, error) {
	return s.sweepOne(ctx, now, "system",
		`state = $1 AND settle_after IS NOT NULL AND settle_after <= $2`,
		[]any{string(pocket.StateDeliveredPending), now},
		pocket.Event{Kind: pocket.EvSettleDue})
}

// sweepOne claims one pocket matching where (skipping rows another transaction
// holds) and applies ev to it in the same transaction. The now used for the
// predicate is the same now handed to the domain guard, so the boundary is
// consistent between the query and the transition.
func (s *Store) sweepOne(ctx context.Context, now time.Time, actor, where string, args []any, ev pocket.Event) (WriteResult, bool, error) {
	var (
		result WriteResult
		found  bool
	)
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		q := `SELECT ` + pocketColumns + ` FROM pockets WHERE ` + where + ` LIMIT 1 FOR UPDATE SKIP LOCKED`
		rec, err := s.scanPocket(tx.QueryRow(ctx, q, args...))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil // nothing due this pass
			}
			return err
		}
		found = true
		result, err = applyTransitionTx(ctx, tx, rec.ID, actor, rec, now, ev)
		return err
	})
	if err != nil {
		return WriteResult{}, false, err
	}
	return result, found, nil
}

// AttestNonReceipt records the buyer's sworn statement that they never received
// the item while the pocket is FROZEN. It changes no domain state; it arms the
// grace-lapse refund (#9), which the sweeper performs only once this flag is set.
// It is idempotent and rejects any pocket not currently frozen.
func (s *Store) AttestNonReceipt(ctx context.Context, pocketID string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE pockets
		SET buyer_nonreceipt_attested_at = COALESCE(buyer_nonreceipt_attested_at, $1), updated_at = now()
		WHERE id = $2 AND state = $3`,
		now, pocketID, string(pocket.StateFrozen))
	if err != nil {
		return fmt.Errorf("attest non-receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIllegalState
	}
	return nil
}
