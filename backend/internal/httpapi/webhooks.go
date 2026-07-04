package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"escrowpay/internal/gateway/nomba"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/store"
)

// handleNombaWebhook ingests provider payment notifications. The HMAC
// signature is the sole authentication: a delivery that does not verify is
// rejected before anything is read from it. Verified events are recorded
// exactly once and processed idempotently, so provider redeliveries and
// replays are harmless. Non-2xx responses put the delivery on the provider's
// retry schedule.
func (a *API) handleNombaWebhook(w http.ResponseWriter, r *http.Request) {
	if a.nombaWebhook == nil {
		a.writeError(w, store.ErrNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		a.writeError(w, errBadRequest)
		return
	}
	ev, err := a.nombaWebhook.Verify(r.Header, body)
	if err != nil {
		if errors.Is(err, nomba.ErrSignature) {
			a.logger.Warn("webhook signature rejected", slog.String("client", a.clientIP(r)))
			a.writeError(w, errUnauthorized)
			return
		}
		a.writeError(w, errBadRequest)
		return
	}
	if err := a.app.IngestGatewayEvent(r.Context(), translateNombaEvent(ev)); err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "received"})
}

// translateNombaEvent maps a verified provider notification onto the
// application's provider-neutral event vocabulary.
func translateNombaEvent(ev nomba.WebhookEvent) pocketapp.GatewayEvent {
	kind := pocketapp.GatewayEventUnknown
	switch ev.Type {
	case "payment_success":
		kind = pocketapp.GatewayFundingSucceeded
	case "payment_failed":
		kind = pocketapp.GatewayFundingFailed
	case "payout_success":
		kind = pocketapp.GatewayPayoutSucceeded
	case "payout_failed":
		kind = pocketapp.GatewayPayoutFailed
	}
	return pocketapp.GatewayEvent{
		ProviderEventID: ev.ID,
		Kind:            kind,
		RawType:         ev.Type,
		OrderRef:        ev.OrderReference,
		MerchantTxRef:   ev.MerchantTxRef,
		PaymentRef:      ev.TransactionID,
		AmountKobo:      ev.AmountKobo,
		Payload:         ev.Payload,
	}
}
