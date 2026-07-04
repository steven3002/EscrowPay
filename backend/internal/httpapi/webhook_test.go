package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testWebhookKey = "test-webhook-signature-key"

func newWebhookEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnv(t, envOptions{sandbox: true, webhookKey: testWebhookKey})
}

// nombaEvent is the scriptable shape of a provider notification. Fields feed
// both the JSON payload and the signature, mirroring how the provider signs.
type nombaEvent struct {
	eventType     string
	requestID     string
	userID        string
	walletID      string
	transactionID string
	txType        string
	txTime        string
	responseCode  string
	orderRef      string
	merchantRef   string
	amountNaira   json.Number
}

func (ev nombaEvent) body(t *testing.T) []byte {
	t.Helper()
	tx := map[string]any{
		"transactionId": ev.transactionID,
		"type":          ev.txType,
		"time":          ev.txTime,
		"responseCode":  ev.responseCode,
	}
	if ev.merchantRef != "" {
		tx["merchantTxRef"] = ev.merchantRef
	}
	if ev.amountNaira != "" {
		tx["transactionAmount"] = ev.amountNaira
	}
	data := map[string]any{
		"merchant":    map[string]any{"userId": ev.userID, "walletId": ev.walletID},
		"transaction": tx,
	}
	if ev.orderRef != "" {
		data["order"] = map[string]any{"orderReference": ev.orderRef}
	}
	b, err := json.Marshal(map[string]any{
		"event_type": ev.eventType,
		"requestId":  ev.requestID,
		"data":       data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (ev nombaEvent) signature(key, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(strings.Join([]string{
		ev.eventType, ev.requestID, ev.userID, ev.walletID,
		ev.transactionID, ev.txType, ev.txTime, ev.responseCode, timestamp,
	}, ":")))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// deliver posts the event to the webhook endpoint signed under key.
func deliver(t *testing.T, e *testEnv, ev nombaEvent, key string) (int, []byte) {
	t.Helper()
	const timestamp = "2026-07-02T12:00:05Z"
	req, err := http.NewRequest("POST", e.server.URL+"/api/webhooks/nomba", bytes.NewReader(ev.body(t)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("nomba-timestamp", timestamp)
	req.Header.Set("nomba-signature", ev.signature(key, timestamp))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// fundedPocket sets up the standard pocket in CREATED and returns its create
// response plus the funding reference the gateway minted for it.
func createdPocketWithRef(t *testing.T, e *testEnv) (cast, createResp, string) {
	t.Helper()
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr)
	rec, err := e.store.GetByID(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatalf("load pocket: %v", err)
	}
	if rec.FundingLinkRef == "" {
		t.Fatal("pocket has no funding reference after acceptance")
	}
	return c, cr, rec.FundingLinkRef
}

// paymentFor builds a signed-shape payment_success event funding the pocket.
func paymentFor(ref, txID string, amount json.Number) nombaEvent {
	return nombaEvent{
		eventType:     "payment_success",
		requestID:     "evt-" + txID,
		userID:        "613bb620-c8e5-45f6-9c00-11223344",
		walletID:      "693e907aad9ea59616aa",
		transactionID: txID,
		txType:        "online_checkout",
		txTime:        "2026-07-02T12:00:00Z",
		responseCode:  "00",
		orderRef:      ref,
		amountNaira:   amount,
	}
}

func pocketState(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	rec, err := e.store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load pocket: %v", err)
	}
	return string(rec.Pocket.State)
}

func TestWebhookFundsPocketExactlyOnce(t *testing.T) {
	e := newWebhookEnv(t)
	_, cr, ref := createdPocketWithRef(t, e)
	ev := paymentFor(ref, "API-CHECKOUT-A1", "10200.00")

	status, data := deliver(t, e, ev, testWebhookKey)
	if status != 200 {
		t.Fatalf("webhook: status %d, body %s", status, data)
	}
	if got := pocketState(t, e, cr.PocketID); got != "FUNDED" {
		t.Fatalf("state = %s, want FUNDED", got)
	}

	rec, err := e.store.GetByID(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.FundingTxID != "API-CHECKOUT-A1" {
		t.Errorf("funding transaction id = %q, want the payment's transaction id", rec.FundingTxID)
	}

	// The provider redelivers; processing must not run twice.
	for i := 0; i < 2; i++ {
		if status, data := deliver(t, e, ev, testWebhookKey); status != 200 {
			t.Fatalf("redelivery %d: status %d, body %s", i, status, data)
		}
	}
	var transitions int
	err = testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pocket_events WHERE pocket_id = $1 AND kind = 'funding_confirmed'`,
		cr.PocketID).Scan(&transitions)
	if err != nil {
		t.Fatal(err)
	}
	if transitions != 1 {
		t.Fatalf("funding transitions = %d, want exactly 1", transitions)
	}

	var processed bool
	err = testPool.QueryRow(context.Background(),
		`SELECT processed_at IS NOT NULL FROM webhook_events WHERE provider_event_id = $1`,
		"evt-API-CHECKOUT-A1").Scan(&processed)
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Error("webhook event should be marked processed")
	}
}

func TestWebhookForgedSignatureRejected(t *testing.T) {
	e := newWebhookEnv(t)
	_, cr, ref := createdPocketWithRef(t, e)
	ev := paymentFor(ref, "API-CHECKOUT-B1", "10200.00")

	status, _ := deliver(t, e, ev, "attacker-key")
	if status != 401 {
		t.Fatalf("forged signature: status %d, want 401", status)
	}
	if got := pocketState(t, e, cr.PocketID); got != "CREATED" {
		t.Fatalf("state = %s, want CREATED untouched", got)
	}
	var events int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM webhook_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("webhook_events rows = %d, want 0 — nothing from a forged delivery may persist", events)
	}
}

func TestWebhookUnderpaymentDoesNotFund(t *testing.T) {
	e := newWebhookEnv(t)
	_, cr, ref := createdPocketWithRef(t, e)
	ev := paymentFor(ref, "API-CHECKOUT-C1", "10199.99")

	status, _ := deliver(t, e, ev, testWebhookKey)
	if status != 200 {
		t.Fatalf("underpayment ack: status %d, want 200", status)
	}
	if got := pocketState(t, e, cr.PocketID); got != "CREATED" {
		t.Fatalf("state = %s, want CREATED", got)
	}
	// The event is retained as evidence but not marked processed.
	var processed bool
	err := testPool.QueryRow(context.Background(),
		`SELECT processed_at IS NOT NULL FROM webhook_events WHERE provider_event_id = $1`,
		"evt-API-CHECKOUT-C1").Scan(&processed)
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Error("underpaid event must stay unprocessed for operator attention")
	}
}

func TestWebhookUnknownOrderReferenceIsAcknowledged(t *testing.T) {
	e := newWebhookEnv(t)
	ev := paymentFor("escrowpay-no-such-pocket", "API-CHECKOUT-D1", "10200.00")

	status, _ := deliver(t, e, ev, testWebhookKey)
	if status != 200 {
		t.Fatalf("unknown reference: status %d, want 200 (ack, keep evidence)", status)
	}
}

// payoutEvent builds a signed-shape payout notification for a settlement leg.
func payoutEvent(eventType, merchantRef, txID string) nombaEvent {
	return nombaEvent{
		eventType:     eventType,
		requestID:     "evt-" + txID,
		userID:        "613bb620-c8e5-45f6-9c00-11223344",
		walletID:      "693e907aad9ea59616aa",
		transactionID: txID,
		txType:        "transfer",
		txTime:        "2026-07-02T13:00:00Z",
		responseCode:  "",
		merchantRef:   merchantRef,
	}
}

func TestWebhookResolvesInflightPayoutLeg(t *testing.T) {
	e := newWebhookEnv(t)
	_, cr, _ := createdPocketWithRef(t, e)
	ctx := context.Background()

	// A leg whose submission ended ambiguously: claimed inflight, no outcome.
	key := cr.PocketID + ":payout:vendor"
	_, err := testPool.Exec(ctx, `
		INSERT INTO settlements (pocket_id, direction, beneficiary_role, amount_kobo, idempotency_key)
		VALUES ($1, 'payout', 'vendor', 1000000, $2)`, cr.PocketID, key)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := e.store.ClaimSettlementLeg(ctx, key)
	if err != nil || !claimed {
		t.Fatalf("claim: %v claimed=%v", err, claimed)
	}
	// A second claim must lose: the leg is already inflight.
	if again, _ := e.store.ClaimSettlementLeg(ctx, key); again {
		t.Fatal("second claim must not win")
	}

	// The provider's payout notification is the reconciler.
	status, data := deliver(t, e, payoutEvent("payout_success", key, "API-TRANSFER-Z9"), testWebhookKey)
	if status != 200 {
		t.Fatalf("payout webhook: status %d, body %s", status, data)
	}

	var legStatus, gatewayRef string
	err = testPool.QueryRow(ctx,
		`SELECT status, COALESCE(gateway_ref,'') FROM settlements WHERE idempotency_key = $1`, key).
		Scan(&legStatus, &gatewayRef)
	if err != nil {
		t.Fatal(err)
	}
	if legStatus != "confirmed" || gatewayRef != "API-TRANSFER-Z9" {
		t.Fatalf("leg = %s/%s, want confirmed/API-TRANSFER-Z9", legStatus, gatewayRef)
	}

	// A confirmed leg is invisible to both reconciliation scans.
	if legs, _ := e.store.PendingSettlementLegs(ctx); len(legs) != 0 {
		t.Fatalf("pending legs = %d, want 0", len(legs))
	}
	if legs, _ := e.store.InflightSettlementLegs(ctx); len(legs) != 0 {
		t.Fatalf("inflight legs = %d, want 0", len(legs))
	}
}

func TestWebhookPayoutFailureMarksLeg(t *testing.T) {
	e := newWebhookEnv(t)
	_, cr, _ := createdPocketWithRef(t, e)
	ctx := context.Background()

	key := cr.PocketID + ":payout:vendor"
	_, err := testPool.Exec(ctx, `
		INSERT INTO settlements (pocket_id, direction, beneficiary_role, amount_kobo, idempotency_key)
		VALUES ($1, 'payout', 'vendor', 1000000, $2)`, cr.PocketID, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimSettlementLeg(ctx, key); err != nil {
		t.Fatal(err)
	}

	status, _ := deliver(t, e, payoutEvent("payout_failed", key, "API-TRANSFER-F1"), testWebhookKey)
	if status != 200 {
		t.Fatalf("payout_failed webhook: status %d", status)
	}
	var legStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM settlements WHERE idempotency_key = $1`, key).Scan(&legStatus); err != nil {
		t.Fatal(err)
	}
	if legStatus != "failed" {
		t.Fatalf("leg status = %s, want failed", legStatus)
	}
}

func TestWebhookEndpointAbsentWithoutVerifier(t *testing.T) {
	e := newTestEnv(t) // no webhook key configured
	ev := paymentFor("escrowpay-x", "API-CHECKOUT-E1", "1.00")
	status, _ := deliver(t, e, ev, testWebhookKey)
	if status != 404 {
		t.Fatalf("status = %d, want 404 when no verifier is configured", status)
	}
}
