package pocketapp

import (
	"context"
	"log/slog"

	"escrowpay/internal/gateway"
	"escrowpay/internal/pocket"
)

// FundingCheck reports the outcome of a pull-side funding verification.
type FundingCheck struct {
	// Funded is true when the pocket is at or past FUNDED after the check —
	// whether this call credited the payment or an earlier notification did.
	Funded bool
	State  pocket.State
}

// VerifyFunding asks the payment provider whether a CREATED pocket's funding
// order has been paid and, if so, credits it through the same idempotent path
// the payment webhook uses. It is the recovery route for payments whose
// notification never arrived — the buyer returning from a hosted checkout, or
// the background sweep. Pockets past CREATED report their standing without a
// provider call; a gateway that cannot answer (the mock default) leaves the
// pocket untouched.
func (a *App) VerifyFunding(ctx context.Context, pocketID string) (FundingCheck, error) {
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return FundingCheck{}, err
	}
	if fundingApplied(rec.Pocket.State) {
		return FundingCheck{Funded: true, State: rec.Pocket.State}, nil
	}
	if rec.Pocket.State != pocket.StateCreated {
		return FundingCheck{State: rec.Pocket.State}, nil
	}
	verifier, ok := a.gateway.(gateway.FundingVerifier)
	if !ok {
		return FundingCheck{State: rec.Pocket.State}, nil
	}

	// Either of the order's references answers: the provider-generated one
	// stored when the link was minted, or the deterministic submitted one.
	refs := []string{rec.FundingLinkRef}
	if own := gateway.FundingRef(rec.ID); own != rec.FundingLinkRef {
		refs = append(refs, own)
	}
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		status, err := verifier.VerifyFunding(ctx, ref)
		if err != nil {
			a.logger.Warn("funding verification query failed",
				slog.String("pocket_id", rec.ID), slog.String("order_ref", ref),
				slog.String("error", err.Error()))
			continue
		}
		if !status.Paid {
			continue
		}
		funded, err := a.creditFunding(ctx, rec, status.AmountKobo, status.PaymentRef)
		if err != nil {
			return FundingCheck{State: rec.Pocket.State}, err
		}
		current, err := a.store.GetByID(ctx, rec.ID)
		if err != nil {
			return FundingCheck{Funded: funded, State: rec.Pocket.State}, nil
		}
		return FundingCheck{Funded: funded, State: current.Pocket.State}, nil
	}
	return FundingCheck{State: rec.Pocket.State}, nil
}

// SweepPendingFundingVerification verifies every CREATED pocket whose funding
// window is still open against the provider, crediting any payment whose
// webhook never landed. It is a no-op on gateways that cannot answer funding
// queries.
func (a *App) SweepPendingFundingVerification(ctx context.Context) (int, error) {
	if _, ok := a.gateway.(gateway.FundingVerifier); !ok {
		return 0, nil
	}
	ids, err := a.store.PocketsAwaitingFunding(ctx, a.now())
	if err != nil {
		return 0, err
	}
	funded := 0
	for _, id := range ids {
		check, err := a.VerifyFunding(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return funded, err
			}
			a.logger.Warn("funding verification sweep item failed",
				slog.String("pocket_id", id), slog.String("error", err.Error()))
			continue
		}
		if check.Funded {
			funded++
		}
	}
	return funded, nil
}
