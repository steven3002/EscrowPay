package pocketapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"escrowpay/internal/gateway"
	"escrowpay/internal/notify"
	"escrowpay/internal/pocket"
	"escrowpay/internal/releasecode"
	"escrowpay/internal/store"
)

// applyOutcome runs a committed transition's side effects: first the notify /
// funding-link / release-code effects, then, when the transition moved money,
// the disbursement of the settlement legs it recorded in-transaction. Both halves
// are idempotent and best-effort — the committed state is authoritative, and the
// sweeper re-derives and retries anything left unfinished by a crash.
func (a *App) applyOutcome(ctx context.Context, pocketID string, out pocket.Outcome) {
	a.executeEffects(ctx, pocketID, out)
	if movesMoney(out) {
		if err := a.paySettlements(ctx, pocketID); err != nil {
			a.logger.Error("settlement disbursement failed",
				slog.String("pocket_id", pocketID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// movesMoney reports whether an outcome recorded any settlement legs to disburse.
func movesMoney(out pocket.Outcome) bool {
	for _, e := range out.Effects {
		switch e.(type) {
		case pocket.SchedulePayout, pocket.ExecuteRefund:
			return true
		}
	}
	return false
}

// executeEffects runs each non-money effect a transition returned, after the
// state change has committed. Effects are best-effort and idempotent: a failure
// is logged, not fatal. The plaintext Release Code is never logged.
func (a *App) executeEffects(ctx context.Context, pocketID string, out pocket.Outcome) {
	buyerTotal := out.Pocket.BuyerTotalKobo()
	for _, e := range out.Effects {
		if err := a.executeEffect(ctx, pocketID, buyerTotal, e); err != nil {
			a.logger.Error("effect execution failed",
				slog.String("pocket_id", pocketID),
				slog.String("effect", fmt.Sprintf("%T", e)),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (a *App) executeEffect(ctx context.Context, pocketID string, buyerTotal int64, e pocket.Effect) error {
	switch eff := e.(type) {
	case pocket.CreateFundingLink:
		rec, err := a.store.GetByID(ctx, pocketID)
		if err != nil {
			return err
		}
		email, _, err := a.store.FundingContact(ctx, pocketID)
		if err != nil {
			return err
		}
		link, err := a.gateway.CreateFundingLink(ctx, gateway.CreateFundingLinkRequest{
			PocketID:      pocketID,
			ShortCode:     rec.ShortCode,
			AmountKobo:    buyerTotal,
			CustomerEmail: email,
			ExpiresAt:     eff.ExpiresAt,
		})
		if err != nil {
			return err
		}
		return a.store.SetFundingLink(ctx, pocketID, link.Ref, link.URL)

	case pocket.GenerateReleaseCode:
		return a.generateReleaseCode(ctx, pocketID)

	case pocket.Notify:
		return a.notifier.Send(ctx, notify.Event{
			PocketID: pocketID,
			Role:     string(eff.Role),
			Kind:     eff.Kind,
			Message:  messageFor(eff.Kind),
		})

	case pocket.PromptEvidence:
		return a.notifier.Send(ctx, notify.Event{
			PocketID: pocketID,
			Role:     string(pocket.RoleBuyer),
			Kind:     "record_unboxing",
			Message:  "Record your unboxing video now to protect your purchase.",
		})

	case pocket.SchedulePayout, pocket.ExecuteRefund:
		// Money movements are persisted as pending settlement legs inside the
		// transition's own transaction (store.recordSettlementLegsTx) and
		// disbursed by paySettlements after commit. Nothing to do here.
		return nil

	case pocket.StartGrace:
		// The grace deadline is persisted by the transition itself; the sweeper
		// acts on it. Nothing further to execute here.
		return nil

	case pocket.Sanction:
		a.logger.Info("sanction recorded",
			slog.String("pocket_id", pocketID),
			slog.String("role", string(eff.Role)),
			slog.String("kind", string(eff.Kind)),
		)
		return a.store.RecordSanction(ctx, pocketID, string(eff.Role))

	default:
		a.logger.Warn("unhandled effect",
			slog.String("pocket_id", pocketID),
			slog.String("effect", fmt.Sprintf("%T", e)),
		)
		return nil
	}
}

// generateReleaseCode mints a code, stores its HMAC verifier and an encrypted
// buyer-retrievable copy, and never reveals the plaintext here. SetReleaseCode
// only writes when no code exists, so replaying this effect is safe.
func (a *App) generateReleaseCode(ctx context.Context, pocketID string) error {
	code, err := releasecode.Generate()
	if err != nil {
		return err
	}
	hash := releasecode.Hash(code, a.releaseCodeSecret)
	enc, err := releasecode.Seal(code, a.releaseCodeSecret)
	if err != nil {
		return err
	}
	return a.store.SetReleaseCode(ctx, pocketID, hash, enc)
}

// paySettlements disburses every pending leg of one pocket. It is the immediate
// post-commit half of two-phase settlement; the sweeper runs the same logic over
// all pockets to recover legs a crash left pending.
func (a *App) paySettlements(ctx context.Context, pocketID string) error {
	legs, err := a.store.PendingSettlementLegsForPocket(ctx, pocketID)
	if err != nil {
		return err
	}
	for _, leg := range legs {
		if err := a.payLeg(ctx, leg); err != nil {
			return err
		}
	}
	return nil
}

// payLeg pushes one settlement leg through the gateway. The claim protocol
// bounds every leg to at most one live submission: claim the leg (pending →
// inflight), call the gateway, confirm on success. A definitive rejection
// fails the leg; a failure that provably never reached the provider releases
// it for retry; any ambiguous failure leaves it inflight, where only the
// provider's payout notification or a status re-query can resolve it. Money
// therefore moves at most once even against a provider that does not enforce
// idempotency keys server-side.
func (a *App) payLeg(ctx context.Context, leg store.SettlementLeg) error {
	claimed, err := a.store.ClaimSettlementLeg(ctx, leg.IdempotencyKey)
	if err != nil {
		return err
	}
	if !claimed {
		// Another worker or a provider notification already advanced this leg.
		return nil
	}

	var ref string
	switch leg.Direction {
	case "payout":
		ref, err = a.gateway.Payout(ctx, gateway.PayoutRequest{
			PocketID:        leg.PocketID,
			BeneficiaryRole: leg.BeneficiaryRole,
			AmountKobo:      leg.AmountKobo,
			IdempotencyKey:  leg.IdempotencyKey,
		})
	case "refund":
		ref, err = a.gateway.Refund(ctx, gateway.RefundRequest{
			PocketID:       leg.PocketID,
			AmountKobo:     leg.AmountKobo,
			IdempotencyKey: leg.IdempotencyKey,
		})
	default:
		return fmt.Errorf("unknown settlement direction %q", leg.Direction)
	}

	switch {
	case err == nil:
		return a.store.ConfirmSettlement(ctx, leg.IdempotencyKey, ref)
	case errors.Is(err, gateway.ErrRejected):
		if failErr := a.store.FailSettlementLeg(ctx, leg.IdempotencyKey, err.Error()); failErr != nil {
			return errors.Join(err, failErr)
		}
		return err
	case errors.Is(err, gateway.ErrNotSubmitted):
		if relErr := a.store.ReleaseSettlementLeg(ctx, leg.IdempotencyKey, err.Error()); relErr != nil {
			return errors.Join(err, relErr)
		}
		return err
	default:
		// Ambiguous: the disbursement may have executed. The leg stays
		// inflight for reconciliation.
		return err
	}
}

// messageFor renders demo notification copy for a transition's notify kind. It
// deliberately carries no sensitive data.
func messageFor(kind string) string {
	switch kind {
	case pocket.NotifyFundsSecured:
		return "Funds are secured in escrow. Deliver the item and collect the buyer's Release Code."
	case pocket.NotifyDeliveryConfirmed:
		return "Delivery confirmed. Your inspection window has started."
	case pocket.NotifyHandoffConfirmed:
		return "Handoff confirmed. Settlement follows once the inspection window closes."
	case pocket.NotifyCodeLocked:
		return "The Release Code is locked after too many attempts. Support has been notified."
	case pocket.NotifyFundingExpired:
		return "The funding window expired and the pocket was closed."
	case pocket.NotifyCancelled:
		return "The pocket was cancelled."
	case pocket.NotifyRefundIssued:
		return "A refund has been issued to the buyer."
	case pocket.NotifyDeliveryWindowClosed:
		return "The delivery window closed without a Release Code. A grace period has started."
	case pocket.NotifyDisputeOpened:
		return "A dispute was opened. Please upload your evidence."
	case pocket.NotifySettled:
		return "The pocket settled and funds were released."
	default:
		return "EscrowPay update."
	}
}
