// Package gateway defines the payment boundary. Everything money-shaped in the
// system calls this interface; implementations decide whether funds move for
// real. Nothing outside this package may assume anything about the underlying
// payment provider.
package gateway

import (
	"context"
	"errors"
	"strings"
	"time"
)

// fundingRefPrefix namespaces the order references this system submits to
// providers, making a funding payment traceable to its pocket even when the
// provider's notification echoes our reference rather than its own.
const fundingRefPrefix = "escrowpay-"

// FundingRef derives the deterministic order reference submitted for a
// pocket's funding. Stability across retries is what makes re-creating a
// funding link benign.
func FundingRef(pocketID string) string {
	return fundingRefPrefix + pocketID
}

// PocketIDFromRef recovers the pocket id from a reference minted by
// FundingRef, reporting whether ref follows that convention.
func PocketIDFromRef(ref string) (string, bool) {
	id, ok := strings.CutPrefix(ref, fundingRefPrefix)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// ErrRejected marks a definitive provider rejection: the request was received,
// refused, and no money moved. Callers may safely treat the operation as
// failed. Any other error is ambiguous — the operation may or may not have
// been executed — and must not be retried blindly.
var ErrRejected = errors.New("gateway: request rejected by provider")

// ErrNotSubmitted marks a failure that provably occurred before the request
// reached the provider (bad configuration, connection never established).
// Callers may safely retry.
var ErrNotSubmitted = errors.New("gateway: request not submitted")

// FundingLink is the checkout artifact a buyer pays into. For a real provider
// this is a dynamic payment link or virtual account; the mock fabricates one.
type FundingLink struct {
	Ref       string
	URL       string
	ExpiresAt time.Time
}

// CreateFundingLinkRequest identifies the pocket a buyer will fund and the
// exact amount the link must collect. ShortCode is the pocket's public handle,
// used by providers that redirect the payer back to the app; CustomerEmail is
// the payer's receipt contact and may be empty.
type CreateFundingLinkRequest struct {
	PocketID      string
	ShortCode     string
	AmountKobo    int64
	CustomerEmail string
	ExpiresAt     time.Time
}

// PayoutRequest is one settlement leg. IdempotencyKey must be unique per leg
// and stable across retries: submitting the same key twice must not move money
// twice.
type PayoutRequest struct {
	PocketID              string
	BeneficiaryRole       string
	BeneficiaryAccountRef string
	AmountKobo            int64
	IdempotencyKey        string
}

// RefundRequest returns escrowed funds to the buyer. Same idempotency
// contract as PayoutRequest.
type RefundRequest struct {
	PocketID       string
	AmountKobo     int64
	IdempotencyKey string
}

// Gateway is the payment provider boundary. All methods are safe to retry;
// idempotency is guaranteed by the request keys, not by caller discipline.
// Providers that cannot enforce idempotency server-side rely on the caller's
// settlement-leg claim protocol never resubmitting an ambiguous request.
type Gateway interface {
	CreateFundingLink(ctx context.Context, req CreateFundingLinkRequest) (FundingLink, error)
	Payout(ctx context.Context, req PayoutRequest) (gatewayRef string, err error)
	Refund(ctx context.Context, req RefundRequest) (gatewayRef string, err error)
}

// PayoutStatus is a provider's answer to whether a previously submitted
// disbursement executed.
type PayoutStatus int

const (
	// PayoutStatusUnknown means the provider cannot answer definitively.
	PayoutStatusUnknown PayoutStatus = iota
	// PayoutStatusConfirmed means the disbursement definitively executed.
	PayoutStatusConfirmed
	// PayoutStatusAbsent means the provider definitively never received it.
	PayoutStatusAbsent
)

// StatusQuerier is an optional interface a Gateway may implement to resolve
// legs whose submission ended ambiguously. The reconciliation sweep uses it,
// when available, to confirm or safely release inflight legs.
type StatusQuerier interface {
	PayoutStatus(ctx context.Context, idempotencyKey string) (PayoutStatus, string, error)
}
