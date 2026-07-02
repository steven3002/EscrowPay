// Package gateway defines the payment boundary. Everything money-shaped in the
// system calls this interface; implementations decide whether funds move for
// real. Nothing outside this package may assume anything about the underlying
// payment provider.
package gateway

import (
	"context"
	"time"
)

// FundingLink is the checkout artifact a buyer pays into. For a real provider
// this is a dynamic payment link or virtual account; the mock fabricates one.
type FundingLink struct {
	Ref       string
	URL       string
	ExpiresAt time.Time
}

// CreateFundingLinkRequest identifies the pocket a buyer will fund and the
// exact amount the link must collect.
type CreateFundingLinkRequest struct {
	PocketID   string
	AmountKobo int64
	ExpiresAt  time.Time
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
type Gateway interface {
	CreateFundingLink(ctx context.Context, req CreateFundingLinkRequest) (FundingLink, error)
	Payout(ctx context.Context, req PayoutRequest) (gatewayRef string, err error)
	Refund(ctx context.Context, req RefundRequest) (gatewayRef string, err error)
}
