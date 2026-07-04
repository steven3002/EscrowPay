package pocketapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"escrowpay/internal/gateway"
	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// GatewayEventKind classifies a provider notification independently of any
// provider's naming.
type GatewayEventKind string

const (
	GatewayFundingSucceeded GatewayEventKind = "funding_succeeded"
	GatewayFundingFailed    GatewayEventKind = "funding_failed"
	GatewayPayoutSucceeded  GatewayEventKind = "payout_succeeded"
	GatewayPayoutFailed     GatewayEventKind = "payout_failed"
	GatewayEventUnknown     GatewayEventKind = "unknown"
)

// GatewayEvent is a verified payment-provider notification, translated by the
// transport layer into provider-neutral terms. Verification (signatures) is
// the transport's job; this layer trusts the event and owns what it means.
type GatewayEvent struct {
	// ProviderEventID is the provider's delivery-independent event identity,
	// the replay-protection key.
	ProviderEventID string
	Kind            GatewayEventKind
	// RawType preserves the provider's own event name for the audit trail.
	RawType string
	// OrderRef maps a funding event to the pocket whose funding link minted it.
	OrderRef string
	// MerchantTxRef maps a payout event to its settlement leg (it echoes the
	// leg's idempotency key).
	MerchantTxRef string
	// PaymentRef is the provider's transaction id for this movement.
	PaymentRef string
	// AmountKobo is the moved amount, 0 when the event did not state one.
	AmountKobo int64
	// Payload is the raw notification body, stored for audit and for
	// recovering events this build could not yet map.
	Payload []byte
}

// IngestGatewayEvent records and processes one provider notification, exactly
// once per provider event id. Processing is idempotent end to end, so a
// redelivery of an event whose handling crashed midway completes safely. A
// returned error tells the transport to fail the delivery and let the
// provider's retry schedule drive the next attempt.
func (a *App) IngestGatewayEvent(ctx context.Context, ev GatewayEvent) error {
	if ev.ProviderEventID == "" {
		return fmt.Errorf("%w: provider event id is required", ErrInvalidInput)
	}
	needsProcessing, err := a.store.RecordWebhookEvent(ctx, ev.ProviderEventID, ev.Payload)
	if err != nil {
		return err
	}
	if !needsProcessing {
		a.logger.Info("gateway event replayed; already processed",
			slog.String("event_id", ev.ProviderEventID), slog.String("type", ev.RawType))
		return nil
	}
	done, err := a.processGatewayEvent(ctx, ev)
	if err != nil {
		return err
	}
	if done {
		return a.store.MarkWebhookProcessed(ctx, ev.ProviderEventID)
	}
	// Acknowledged but not resolvable by this build (unmapped reference,
	// unexpected state). The stored payload keeps the evidence; leaving the
	// event unprocessed marks it for operator attention.
	return nil
}

func (a *App) processGatewayEvent(ctx context.Context, ev GatewayEvent) (done bool, err error) {
	switch ev.Kind {
	case GatewayFundingSucceeded:
		return a.applyFundingEvent(ctx, ev)

	case GatewayPayoutSucceeded:
		if ev.MerchantTxRef == "" {
			a.logger.Warn("payout success without a merchant reference",
				slog.String("event_id", ev.ProviderEventID))
			return false, nil
		}
		if err := a.store.ConfirmSettlement(ctx, ev.MerchantTxRef, ev.PaymentRef); err != nil {
			return false, err
		}
		return true, nil

	case GatewayPayoutFailed:
		if ev.MerchantTxRef == "" {
			a.logger.Warn("payout failure without a merchant reference",
				slog.String("event_id", ev.ProviderEventID))
			return false, nil
		}
		a.logger.Error("provider reported payout failure",
			slog.String("leg", ev.MerchantTxRef), slog.String("payment_ref", ev.PaymentRef))
		if err := a.store.FailSettlementLeg(ctx, ev.MerchantTxRef, "provider reported payout failure"); err != nil {
			return false, err
		}
		return true, nil

	case GatewayFundingFailed:
		// The attempt failed at the provider; the pocket stays CREATED and the
		// same funding link remains payable until the funding window closes.
		a.logger.Info("funding attempt failed at provider",
			slog.String("order_ref", ev.OrderRef), slog.String("event_id", ev.ProviderEventID))
		return true, nil

	default:
		a.logger.Info("gateway event recorded without handling",
			slog.String("type", ev.RawType), slog.String("event_id", ev.ProviderEventID))
		return true, nil
	}
}

// applyFundingEvent drives CREATED → FUNDED for the pocket whose funding link
// the payment settled, through the same transition executor every other write
// uses. Replays and out-of-order deliveries degrade to no-ops.
func (a *App) applyFundingEvent(ctx context.Context, ev GatewayEvent) (bool, error) {
	if ev.OrderRef == "" {
		a.logger.Warn("payment notification without an order reference",
			slog.String("event_id", ev.ProviderEventID))
		return false, nil
	}
	// The provider may echo either reference of the order: its own generated
	// one (stored as the pocket's funding reference) or the deterministic one
	// this system submitted, which encodes the pocket id.
	rec, err := a.store.GetByFundingRef(ctx, ev.OrderRef)
	if errors.Is(err, store.ErrNotFound) {
		if id, ok := gateway.PocketIDFromRef(ev.OrderRef); ok {
			rec, err = a.store.GetByID(ctx, id)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		a.logger.Warn("payment notification for an unknown order reference",
			slog.String("order_ref", ev.OrderRef), slog.String("event_id", ev.ProviderEventID))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ev.AmountKobo > 0 && ev.AmountKobo < rec.Pocket.BuyerTotalKobo() {
		a.logger.Error("payment amount below the pocket's buyer total; not funding",
			slog.String("pocket_id", rec.ID),
			slog.Int64("paid_kobo", ev.AmountKobo),
			slog.Int64("expected_kobo", rec.Pocket.BuyerTotalKobo()))
		return false, nil
	}

	res, err := a.store.RunTransition(ctx, rec.ID, "system", pocket.Event{Kind: pocket.EvFundingConfirmed}, a.now())
	switch {
	case err == nil:
		a.applyOutcome(ctx, res.PocketID, res.Outcome)
	case errors.Is(err, pocket.ErrIllegalTransition), errors.Is(err, pocket.ErrTerminal), errors.Is(err, store.ErrIllegalState):
		current, loadErr := a.store.GetByID(ctx, rec.ID)
		if loadErr != nil {
			return false, loadErr
		}
		if !fundingApplied(current.Pocket.State) {
			// Money arrived for a pocket that already closed (expired or
			// cancelled). Nothing here may move it; the payment needs an
			// operator-driven refund.
			a.logger.Error("payment received for a closed pocket; operator refund required",
				slog.String("pocket_id", rec.ID), slog.String("state", string(current.Pocket.State)),
				slog.String("payment_ref", ev.PaymentRef))
			return false, nil
		}
	default:
		return false, err
	}

	if err := a.store.SetFundingTransaction(ctx, rec.ID, ev.PaymentRef); err != nil {
		return false, err
	}
	return true, nil
}

// fundingApplied reports whether a state is at or past FUNDED on the pocket
// lifecycle, i.e. the funding this event reports has already been credited.
func fundingApplied(s pocket.State) bool {
	switch s {
	case pocket.StateFunded, pocket.StateDeliveredPending, pocket.StateSettled,
		pocket.StateDisputed, pocket.StateFrozen, pocket.StateRefunded:
		return true
	default:
		return false
	}
}
