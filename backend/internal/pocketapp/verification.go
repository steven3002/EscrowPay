package pocketapp

import (
	"context"
	"errors"

	"escrowpay/internal/pocket"
	"escrowpay/internal/releasecode"
	"escrowpay/internal/store"
)

// CodeEntryResult reports the outcome of a vendor's Release Code entry. When the
// code matches, the pocket has advanced (to DELIVERED_PENDING, or straight to
// SETTLED in instant mode); when it does not, AttemptsRemaining and Locked
// describe the lockout state after the failed attempt.
type CodeEntryResult struct {
	Accepted          bool
	State             pocket.State
	Locked            bool
	AttemptsRemaining int
}

// EnterCode verifies a Release Code the vendor collected at handoff and drives
// the resulting transition. A correct code fires #6/#8 (FUNDED|FROZEN →
// DELIVERED_PENDING) and, in instant mode, settles synchronously (#11). A wrong
// code fires the attempt policy, locking entry after MaxAttempts. Verification is
// against the stored HMAC; the plaintext is never compared in the clear here.
func (a *App) EnterCode(ctx context.Context, pocketID string, role pocket.Role, code string) (CodeEntryResult, error) {
	if role != pocket.RoleVendor {
		return CodeEntryResult{}, ErrForbidden
	}
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return CodeEntryResult{}, err
	}
	if rec.ReleaseCodeHash == "" {
		return CodeEntryResult{}, ErrCodeNotReady
	}

	if releasecode.Verify(rec.ReleaseCodeHash, code, a.releaseCodeSecret) {
		return a.acceptCode(ctx, pocketID, role)
	}
	return a.rejectCode(ctx, pocketID, role)
}

// acceptCode applies a validated Release Code and settles instantly when the
// pocket's inspection window is zero.
func (a *App) acceptCode(ctx context.Context, pocketID string, role pocket.Role) (CodeEntryResult, error) {
	res, err := a.store.RunTransition(ctx, pocketID, string(role), pocket.Event{Kind: pocket.EvCodeAccepted}, a.now())
	if err != nil {
		return CodeEntryResult{}, err
	}
	a.applyOutcome(ctx, pocketID, res.Outcome)

	state := res.Outcome.Pocket.State
	if res.Outcome.Pocket.DueForSettlement(a.now()) {
		settled, err := a.store.RunTransition(ctx, pocketID, string(role), pocket.Event{Kind: pocket.EvSettleDue}, a.now())
		if err != nil && !errors.Is(err, pocket.ErrNotYetDue) {
			return CodeEntryResult{}, err
		}
		if err == nil {
			a.applyOutcome(ctx, pocketID, settled.Outcome)
			state = settled.Outcome.Pocket.State
		}
	}
	return CodeEntryResult{Accepted: true, State: state}, nil
}

// rejectCode records a failed Release Code entry and reports the lockout state.
func (a *App) rejectCode(ctx context.Context, pocketID string, role pocket.Role) (CodeEntryResult, error) {
	res, err := a.store.RunTransition(ctx, pocketID, string(role), pocket.Event{Kind: pocket.EvCodeRejected}, a.now())
	if err != nil {
		return CodeEntryResult{}, err
	}
	a.applyOutcome(ctx, pocketID, res.Outcome)

	p := res.Outcome.Pocket
	remaining := releasecode.MaxAttempts - p.CodeAttempts
	if remaining < 0 {
		remaining = 0
	}
	return CodeEntryResult{
		Accepted:          false,
		State:             p.State,
		Locked:            p.CodeLocked,
		AttemptsRemaining: remaining,
	}, nil
}

// ReportIssue opens a dispute on behalf of the buyer while the inspection window
// is still open (transition #12). At or after settlement the guard rejects it.
func (a *App) ReportIssue(ctx context.Context, pocketID string, role pocket.Role) (store.WriteResult, error) {
	if role != pocket.RoleBuyer {
		return store.WriteResult{}, ErrForbidden
	}
	res, err := a.store.RunTransition(ctx, pocketID, string(role), pocket.Event{Kind: pocket.EvBuyerReportIssue}, a.now())
	if err != nil {
		return store.WriteResult{}, err
	}
	a.applyOutcome(ctx, pocketID, res.Outcome)
	return res, nil
}

// ConfirmDispatchFailure lets the vendor concede a failed delivery while the
// pocket is FROZEN, refunding the buyer immediately (transition #9,
// reason "vendor_failure") rather than waiting out the grace period.
func (a *App) ConfirmDispatchFailure(ctx context.Context, pocketID string, role pocket.Role) (store.WriteResult, error) {
	if role != pocket.RoleVendor {
		return store.WriteResult{}, ErrForbidden
	}
	res, err := a.store.RunTransition(ctx, pocketID, string(role),
		pocket.Event{Kind: pocket.EvRefundFrozen, Reason: "vendor_failure"}, a.now())
	if err != nil {
		return store.WriteResult{}, err
	}
	a.applyOutcome(ctx, pocketID, res.Outcome)
	return res, nil
}

// AttestNonReceipt records the buyer's non-receipt attestation on a FROZEN
// pocket. It arms the grace-lapse refund (#9) without moving the pocket itself;
// the sweeper refunds only once this is on record and the grace period lapses.
func (a *App) AttestNonReceipt(ctx context.Context, pocketID string, role pocket.Role) error {
	if role != pocket.RoleBuyer {
		return ErrForbidden
	}
	return a.store.AttestNonReceipt(ctx, pocketID, a.now())
}
