package pocketapp

import (
	"context"
	"fmt"
	"log/slog"

	"escrowpay/internal/gateway"
	"escrowpay/internal/notify"
	"escrowpay/internal/pocket"
	"escrowpay/internal/releasecode"
	"escrowpay/internal/store"
)

// executeEffects runs each effect a transition returned, after the state change
// has committed. Effects are best-effort and idempotent: a failure is logged,
// not fatal, because the committed state is authoritative and the settlement
// sweeper (s4) re-derives and retries unfinished money movements. The plaintext
// Release Code is never logged.
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
		link, err := a.gateway.CreateFundingLink(ctx, gateway.CreateFundingLinkRequest{
			PocketID:   pocketID,
			AmountKobo: buyerTotal,
			ExpiresAt:  eff.ExpiresAt,
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

	case pocket.SchedulePayout:
		return a.schedulePayout(ctx, pocketID, eff.Legs)

	case pocket.ExecuteRefund:
		return a.executeRefund(ctx, pocketID, eff)

	case pocket.StartGrace:
		// The grace deadline is persisted by the transition itself; the sweeper
		// (s4) acts on it. Nothing further to execute here.
		return nil

	case pocket.Sanction:
		// Enforcement bookkeeping is finalized with disputes (s5); record intent.
		a.logger.Info("sanction recorded",
			slog.String("pocket_id", pocketID),
			slog.String("role", string(eff.Role)),
			slog.String("kind", string(eff.Kind)),
		)
		return nil

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

// schedulePayout disburses each settlement leg with a stable idempotency key so
// retries never move money twice.
func (a *App) schedulePayout(ctx context.Context, pocketID string, legs []pocket.PayoutLeg) error {
	for _, leg := range legs {
		key := fmt.Sprintf("%s:payout:%s", pocketID, leg.Role)
		if _, err := a.store.RecordSettlementLeg(ctx, store.SettlementLeg{
			PocketID:        pocketID,
			Direction:       "payout",
			BeneficiaryRole: string(leg.Role),
			AmountKobo:      leg.AmountKobo,
			IdempotencyKey:  key,
		}); err != nil {
			return err
		}
		ref, err := a.gateway.Payout(ctx, gateway.PayoutRequest{
			PocketID:        pocketID,
			BeneficiaryRole: string(leg.Role),
			AmountKobo:      leg.AmountKobo,
			IdempotencyKey:  key,
		})
		if err != nil {
			return err
		}
		if err := a.store.ConfirmSettlement(ctx, key, ref); err != nil {
			return err
		}
	}
	return nil
}

// executeRefund returns the buyer's funds as a single settlement leg.
func (a *App) executeRefund(ctx context.Context, pocketID string, eff pocket.ExecuteRefund) error {
	key := fmt.Sprintf("%s:refund:%s", pocketID, eff.BeneficiaryRole)
	if _, err := a.store.RecordSettlementLeg(ctx, store.SettlementLeg{
		PocketID:        pocketID,
		Direction:       "refund",
		BeneficiaryRole: string(eff.BeneficiaryRole),
		AmountKobo:      eff.AmountKobo,
		IdempotencyKey:  key,
	}); err != nil {
		return err
	}
	ref, err := a.gateway.Refund(ctx, gateway.RefundRequest{
		PocketID:       pocketID,
		AmountKobo:     eff.AmountKobo,
		IdempotencyKey: key,
	})
	if err != nil {
		return err
	}
	return a.store.ConfirmSettlement(ctx, key, ref)
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
