package pocketapp

import (
	"context"
	"fmt"
	"io"
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// evidenceTypes is the closed set of evidence kinds a participant may attach,
// matching the database CHECK constraint.
var evidenceTypes = map[string]bool{
	"unboxing_video": true,
	"dispatch_proof": true,
	"packing_media":  true,
	"photo":          true,
}

// OpenFrozenDispute escalates a FROZEN pocket to arbitration when the buyer
// disputes the vendor's claim of delivery (transition #10, class not_delivered).
func (a *App) OpenFrozenDispute(ctx context.Context, pocketID string, role pocket.Role) (store.WriteResult, error) {
	if role != pocket.RoleBuyer {
		return store.WriteResult{}, ErrForbidden
	}
	return a.runDispute(ctx, pocketID, role, pocket.Event{Kind: pocket.EvFrozenDispute})
}

// Concede resolves a dispute in the buyer's favour when the vendor accepts fault
// (transition #13 → REFUNDED). Vendor-only.
func (a *App) Concede(ctx context.Context, pocketID string, role pocket.Role) (store.WriteResult, error) {
	if role != pocket.RoleVendor {
		return store.WriteResult{}, ErrForbidden
	}
	return a.runDispute(ctx, pocketID, role, pocket.Event{Kind: pocket.EvVendorConcede})
}

// ForceRefund is the admin arbitration outcome against the vendor: refund the
// buyer and flag the vendor for fraud (transition #14).
func (a *App) ForceRefund(ctx context.Context, pocketID string) (store.WriteResult, error) {
	return a.runDispute(ctx, pocketID, "admin", pocket.Event{Kind: pocket.EvAdminForceRefund})
}

// ForcePayout is the admin arbitration outcome against the buyer: release funds
// to the vendor, optionally striking the buyer for bad faith (transition #15).
func (a *App) ForcePayout(ctx context.Context, pocketID string, badFaith bool) (store.WriteResult, error) {
	return a.runDispute(ctx, pocketID, "admin", pocket.Event{Kind: pocket.EvAdminForcePayout, BadFaith: badFaith})
}

// runDispute applies a dispute-lifecycle event through the single write path and
// executes the resulting effects (refund/payout legs, sanctions, notifications).
func (a *App) runDispute(ctx context.Context, pocketID string, actor pocket.Role, ev pocket.Event) (store.WriteResult, error) {
	res, err := a.store.RunTransition(ctx, pocketID, string(actor), ev, a.now())
	if err != nil {
		return store.WriteResult{}, err
	}
	a.applyOutcome(ctx, pocketID, res.Outcome)
	return res, nil
}

// UploadEvidence stores a piece of dispute media and records it. For an in-app
// unboxing video the protection window is enforced server-side: within_window is
// true only when the capture falls within EvidenceCaptureWindow of the handoff
// (transition #6/#8). Evidence is accepted only once a pocket has reached a state
// where it is meaningful (delivered, disputed, or frozen).
func (a *App) UploadEvidence(ctx context.Context, pocketID string, role pocket.Role, evType, filename string, r io.Reader) (store.EvidenceRecord, error) {
	if !evidenceTypes[evType] {
		return store.EvidenceRecord{}, fmt.Errorf("%w: unknown evidence type %q", ErrInvalidInput, evType)
	}
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return store.EvidenceRecord{}, err
	}
	if !evidenceAllowed(rec.Pocket.State) {
		return store.EvidenceRecord{}, fmt.Errorf("%w: evidence not accepted in state %s", ErrInvalidInput, rec.Pocket.State)
	}

	ref, _, err := a.evidence.Put(ctx, pocketID, filename, r, a.evidenceMaxBytes)
	if err != nil {
		return store.EvidenceRecord{}, err
	}

	capturedAt := a.now()
	ev := store.EvidenceRecord{
		Party:         string(role),
		Type:          evType,
		StorageRef:    ref,
		CapturedInApp: true,
		CapturedAt:    capturedAt,
		WithinWindow:  a.unboxingWithinWindow(rec.Pocket, evType, capturedAt),
	}
	id, err := a.store.InsertEvidence(ctx, pocketID, ev)
	if err != nil {
		return store.EvidenceRecord{}, err
	}
	ev.ID = id
	return ev, nil
}

// unboxingWithinWindow computes within_window for an unboxing video: true when
// the capture is within the protection window of the handoff, false when it is
// late, and nil for any other evidence type or when no handoff has occurred. The
// handoff instant is recovered from the pocket's own clock — SettleAfter less the
// inspection window is exactly the moment code entry (#6/#8) set it — so the check
// is consistent with the injected clock rather than a database wall-clock.
func (a *App) unboxingWithinWindow(p pocket.Pocket, evType string, capturedAt time.Time) *bool {
	if evType != "unboxing_video" || p.SettleAfter.IsZero() {
		return nil
	}
	handoff := p.SettleAfter.Add(-p.InspectionWindow)
	within := capturedAt.Sub(handoff) <= a.evidenceCaptureWindow
	return &within
}

// DisputeView returns a pocket's dispute record and its evidence, for the
// participant and admin dispute surfaces.
func (a *App) DisputeView(ctx context.Context, pocketID string) (store.DisputeRecord, []store.EvidenceRecord, error) {
	dispute, err := a.store.GetDispute(ctx, pocketID)
	if err != nil {
		return store.DisputeRecord{}, nil, err
	}
	evidence, err := a.store.EvidenceForPocket(ctx, pocketID)
	if err != nil {
		return store.DisputeRecord{}, nil, err
	}
	return dispute, evidence, nil
}

// ListDisputes returns the open-dispute arbitration queue.
func (a *App) ListDisputes(ctx context.Context) ([]store.DisputeQueueItem, error) {
	return a.store.ListOpenDisputes(ctx)
}

// Dispute returns a pocket's dispute record (store.ErrNotFound if none), for the
// admin surface which authenticates by sandbox gate rather than a link token.
func (a *App) Dispute(ctx context.Context, pocketID string) (store.DisputeRecord, error) {
	return a.store.GetDispute(ctx, pocketID)
}

// Evidence returns all evidence attached to a pocket, for the admin viewer.
func (a *App) Evidence(ctx context.Context, pocketID string) ([]store.EvidenceRecord, error) {
	return a.store.EvidenceForPocket(ctx, pocketID)
}

// evidenceAllowed reports whether a pocket's state accepts evidence uploads.
func evidenceAllowed(s pocket.State) bool {
	switch s {
	case pocket.StateDeliveredPending, pocket.StateDisputed, pocket.StateFrozen:
		return true
	default:
		return false
	}
}
