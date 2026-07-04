package nomba

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const testKey = "test-signature-key"

// samplePayment mirrors the provider's documented payment_success payload with
// a checkout order reference attached.
const samplePayment = `{
  "event_type": "payment_success",
  "requestId": "49e11b44-909b-4f83-82b4-9a83a1234567",
  "data": {
    "merchant": {
      "walletId": "693e907aad9ea59616aa",
      "walletBalance": 539.4,
      "userId": "613bb620-c8e5-45f6-9c00-11223344"
    },
    "order": {
      "orderReference": "escrowpay-8b6f2c1d-0000-4000-8000-123456789abc",
      "amount": 10200.00
    },
    "transaction": {
      "fee": 0.6,
      "type": "online_checkout",
      "transactionId": "API-CHECKOUT-613BB-eeae578a",
      "responseCode": "",
      "transactionAmount": 10200.00,
      "time": "2026-07-02T12:00:00Z"
    }
  }
}`

func sign(t *testing.T, key string, fields ...string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(strings.Join(fields, ":")))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func paymentHeaders(t *testing.T, key string) http.Header {
	t.Helper()
	const ts = "2026-07-02T12:00:05Z"
	h := http.Header{}
	h.Set("nomba-timestamp", ts)
	h.Set("nomba-signature", sign(t, key,
		"payment_success",
		"49e11b44-909b-4f83-82b4-9a83a1234567",
		"613bb620-c8e5-45f6-9c00-11223344",
		"693e907aad9ea59616aa",
		"API-CHECKOUT-613BB-eeae578a",
		"online_checkout",
		"2026-07-02T12:00:00Z",
		"",
		ts,
	))
	return h
}

func TestVerifyExtractsPaymentEvent(t *testing.T) {
	v := NewWebhookVerifier([]byte(testKey))
	ev, err := v.Verify(paymentHeaders(t, testKey), []byte(samplePayment))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.ID != "49e11b44-909b-4f83-82b4-9a83a1234567" {
		t.Errorf("ID = %q", ev.ID)
	}
	if ev.Type != "payment_success" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.OrderReference != "escrowpay-8b6f2c1d-0000-4000-8000-123456789abc" {
		t.Errorf("OrderReference = %q", ev.OrderReference)
	}
	if ev.TransactionID != "API-CHECKOUT-613BB-eeae578a" {
		t.Errorf("TransactionID = %q", ev.TransactionID)
	}
	if ev.AmountKobo != 1020000 {
		t.Errorf("AmountKobo = %d, want 1020000", ev.AmountKobo)
	}
}

func TestVerifyRejectsWrongKeyAndTamper(t *testing.T) {
	v := NewWebhookVerifier([]byte(testKey))

	if _, err := v.Verify(paymentHeaders(t, "other-key"), []byte(samplePayment)); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong key: err = %v, want ErrSignature", err)
	}

	// The provider signs the event identity fields, not the whole body:
	// tampering the transaction id must fail verification. (The amount and
	// order reference are notably outside the signed string, which is why
	// processing never trusts them beyond mapping.)
	tampered := strings.Replace(samplePayment, "API-CHECKOUT-613BB-eeae578a", "API-CHECKOUT-EVIL", 1)
	if _, err := v.Verify(paymentHeaders(t, testKey), []byte(tampered)); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered transaction id: err = %v, want ErrSignature", err)
	}

	h := paymentHeaders(t, testKey)
	h.Set("nomba-timestamp", "2029-01-01T00:00:00Z")
	if _, err := v.Verify(h, []byte(samplePayment)); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered timestamp: err = %v, want ErrSignature", err)
	}

	h = paymentHeaders(t, testKey)
	h.Del("nomba-signature")
	if _, err := v.Verify(h, []byte(samplePayment)); !errors.Is(err, ErrSignature) {
		t.Fatalf("missing signature: err = %v, want ErrSignature", err)
	}
}

func TestVerifySignatureValueFallbackHeader(t *testing.T) {
	v := NewWebhookVerifier([]byte(testKey))
	h := paymentHeaders(t, testKey)
	h.Set("nomba-sig-value", h.Get("nomba-signature"))
	h.Del("nomba-signature")
	if _, err := v.Verify(h, []byte(samplePayment)); err != nil {
		t.Fatalf("nomba-sig-value fallback: %v", err)
	}
}

func TestVerifyPayoutEventCarriesMerchantRef(t *testing.T) {
	const payout = `{
	  "event_type": "payout_success",
	  "requestId": "76a7df87-4819-493c-90ee-11223344",
	  "data": {
	    "merchant": {"walletId": "693e907aad9ea59aaaa", "userId": "613bb620-c8e5-45f6-9c00-11223344"},
	    "transaction": {
	      "type": "transfer",
	      "transactionId": "API-TRANSFER-057A0-21e353c0",
	      "responseCode": "",
	      "merchantTxRef": "pocket-1:payout:vendor",
	      "transactionAmount": 9800,
	      "time": "2026-07-02T13:00:00Z"
	    }
	  }
	}`
	const ts = "2026-07-02T13:00:02Z"
	h := http.Header{}
	h.Set("nomba-timestamp", ts)
	h.Set("nomba-signature", sign(t, testKey,
		"payout_success", "76a7df87-4819-493c-90ee-11223344",
		"613bb620-c8e5-45f6-9c00-11223344", "693e907aad9ea59aaaa",
		"API-TRANSFER-057A0-21e353c0", "transfer", "2026-07-02T13:00:00Z", "", ts,
	))

	ev, err := NewWebhookVerifier([]byte(testKey)).Verify(h, []byte(payout))
	if err != nil {
		t.Fatalf("verify payout: %v", err)
	}
	if ev.MerchantTxRef != "pocket-1:payout:vendor" {
		t.Errorf("MerchantTxRef = %q", ev.MerchantTxRef)
	}
	if ev.AmountKobo != 980000 {
		t.Errorf("AmountKobo = %d, want 980000", ev.AmountKobo)
	}
}
