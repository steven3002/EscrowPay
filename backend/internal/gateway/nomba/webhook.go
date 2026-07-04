package nomba

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// ErrSignature is returned for a webhook whose signature does not verify.
// Callers must reject the delivery without processing anything from it.
var ErrSignature = errors.New("nomba: webhook signature mismatch")

// WebhookEvent is one verified provider notification, reduced to the fields
// event processing needs. Payload retains the raw body for storage and later
// inspection.
type WebhookEvent struct {
	// ID is the provider's event identity (requestId), the replay-protection key.
	ID string
	// Type is the event_type, e.g. payment_success or payout_failed.
	Type string
	// TransactionID is the provider's transaction identity.
	TransactionID string
	// TransactionType is the provider's transaction classification.
	TransactionType string
	// MerchantTxRef echoes the reference we submitted with a transfer; it maps
	// payout events back to their settlement leg.
	MerchantTxRef string
	// OrderReference maps a payment event back to the checkout order that
	// collected it, extracted tolerantly from the fields observed to carry it.
	OrderReference string
	// AmountKobo is the transaction amount converted from naira, 0 when absent.
	AmountKobo int64
	// Payload is the raw request body.
	Payload []byte
}

// WebhookVerifier authenticates webhook deliveries with the signature key
// configured in the provider dashboard.
type WebhookVerifier struct {
	key []byte
}

func NewWebhookVerifier(key []byte) *WebhookVerifier {
	return &WebhookVerifier{key: key}
}

// webhookBody mirrors the payload fields verification and routing need. Every
// leaf is captured raw and coerced afterwards, so a field arriving as a number
// instead of a string never fails the decode.
type webhookBody struct {
	EventType      json.RawMessage `json:"event_type"`
	RequestID      json.RawMessage `json:"requestId"`
	OrderReference json.RawMessage `json:"orderReference"`
	Data           struct {
		OrderReference json.RawMessage `json:"orderReference"`
		Order          struct {
			OrderReference json.RawMessage `json:"orderReference"`
			OrderID        json.RawMessage `json:"orderId"`
			Amount         json.RawMessage `json:"amount"`
		} `json:"order"`
		Merchant struct {
			UserID   json.RawMessage `json:"userId"`
			WalletID json.RawMessage `json:"walletId"`
		} `json:"merchant"`
		Transaction struct {
			TransactionID     json.RawMessage `json:"transactionId"`
			Type              json.RawMessage `json:"type"`
			Time              json.RawMessage `json:"time"`
			ResponseCode      json.RawMessage `json:"responseCode"`
			MerchantTxRef     json.RawMessage `json:"merchantTxRef"`
			OrderReference    json.RawMessage `json:"orderReference"`
			OrderID           json.RawMessage `json:"orderId"`
			TransactionAmount json.RawMessage `json:"transactionAmount"`
		} `json:"transaction"`
	} `json:"data"`
}

// Verify authenticates one delivery and returns its routed event. The signed
// string is the colon-joined sequence
//
//	event_type:requestId:userId:walletId:transactionId:type:time:responseCode:timestamp
//
// where the first eight values come from the payload (empty when absent) and
// the timestamp is the nomba-timestamp header, MACed with HMAC-SHA256 and
// Base64-encoded into the nomba-signature header.
func (v *WebhookVerifier) Verify(h http.Header, body []byte) (WebhookEvent, error) {
	sig := h.Get("nomba-signature")
	if sig == "" {
		sig = h.Get("nomba-sig-value")
	}
	if sig == "" {
		return WebhookEvent{}, fmt.Errorf("missing signature header: %w", ErrSignature)
	}

	var b webhookBody
	if err := json.Unmarshal(body, &b); err != nil {
		return WebhookEvent{}, fmt.Errorf("nomba: webhook body is not valid JSON: %w", err)
	}

	tx := b.Data.Transaction
	signed := strings.Join([]string{
		stringField(b.EventType),
		stringField(b.RequestID),
		stringField(b.Data.Merchant.UserID),
		stringField(b.Data.Merchant.WalletID),
		stringField(tx.TransactionID),
		stringField(tx.Type),
		stringField(tx.Time),
		stringField(tx.ResponseCode),
		h.Get("nomba-timestamp"),
	}, ":")

	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(signed))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return WebhookEvent{}, ErrSignature
	}

	ev := WebhookEvent{
		ID:              stringField(b.RequestID),
		Type:            stringField(b.EventType),
		TransactionID:   stringField(tx.TransactionID),
		TransactionType: stringField(tx.Type),
		MerchantTxRef:   stringField(tx.MerchantTxRef),
		OrderReference: firstNonEmpty(
			stringField(b.Data.Order.OrderReference),
			stringField(tx.OrderReference),
			stringField(b.Data.OrderReference),
			stringField(b.OrderReference),
			stringField(b.Data.Order.OrderID),
			stringField(tx.OrderID),
		),
		AmountKobo: koboAmount(firstNonEmpty(stringField(tx.TransactionAmount), stringField(b.Data.Order.Amount))),
		Payload:    body,
	}
	if ev.ID == "" {
		if ev.TransactionID == "" {
			return WebhookEvent{}, errors.New("nomba: webhook carries no event identity")
		}
		ev.ID = ev.Type + ":" + ev.TransactionID
	}
	return ev, nil
}

// stringField coerces a raw JSON leaf to the string the signature was computed
// over: quoted strings are unquoted, numbers and booleans keep their literal
// form, and null or absent values become empty.
func stringField(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var out string
		if err := json.Unmarshal(raw, &out); err == nil {
			return out
		}
	}
	return s
}

// koboAmount converts a naira-decimal literal to kobo, rounding to the nearest
// kobo. Unparseable input yields 0, which processing treats as "amount not
// stated".
func koboAmount(naira string) int64 {
	if naira == "" {
		return 0
	}
	f, err := strconv.ParseFloat(naira, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int64(math.Round(f * 100))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
