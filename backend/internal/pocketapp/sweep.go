package pocketapp

import (
	"context"
	"time"

	"escrowpay/internal/store"
)

// The Sweep* methods are the application-level driver the settlement sweeper
// calls each tick. Each drains one clock-triggered rule to completion and
// returns how many pockets it advanced, so the sweeper can report progress.

// SweepExpiredFunding expires every CREATED pocket past its funding window (#3).
func (a *App) SweepExpiredFunding(ctx context.Context) (int, error) {
	return a.drainRule(ctx, a.store.SweepOneExpiredFunding)
}

// SweepDeliveryDeadlines freezes every FUNDED pocket past its delivery deadline
// (#7).
func (a *App) SweepDeliveryDeadlines(ctx context.Context) (int, error) {
	return a.drainRule(ctx, a.store.SweepOneDeliveryDeadline)
}

// SweepGraceRefunds refunds every FROZEN pocket whose grace period lapsed with a
// buyer non-receipt attestation on record (#9).
func (a *App) SweepGraceRefunds(ctx context.Context) (int, error) {
	return a.drainRule(ctx, a.store.SweepOneGraceRefund)
}

// SweepDueSettlements settles every DELIVERED_PENDING pocket whose inspection
// window has elapsed (#11).
func (a *App) SweepDueSettlements(ctx context.Context) (int, error) {
	return a.drainRule(ctx, a.store.SweepOneDueSettlement)
}

// SweepPendingSettlementLegs disburses any settlement leg still pending across
// all pockets. It is the reconciliation net: a leg persisted in-transaction but
// left unpaid by a crash between commit and disbursement is paid here, exactly
// once, on the next pass.
func (a *App) SweepPendingSettlementLegs(ctx context.Context) (int, error) {
	legs, err := a.store.PendingSettlementLegs(ctx)
	if err != nil {
		return 0, err
	}
	paid := 0
	for _, leg := range legs {
		if err := a.payLeg(ctx, leg); err != nil {
			return paid, err
		}
		paid++
	}
	return paid, nil
}

// drainRule repeatedly claims and advances one due pocket until the scan finds
// nothing, executing each transition's effects (including disbursement). Every
// claim is an independent transaction, so a mid-drain crash simply leaves the
// remaining pockets for the next tick.
func (a *App) drainRule(ctx context.Context, claim func(context.Context, time.Time) (store.WriteResult, bool, error)) (int, error) {
	processed := 0
	for {
		res, found, err := claim(ctx, a.now())
		if err != nil {
			return processed, err
		}
		if !found {
			return processed, nil
		}
		a.applyOutcome(ctx, res.PocketID, res.Outcome)
		processed++
	}
}
