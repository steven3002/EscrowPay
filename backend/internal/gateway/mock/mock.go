// Package mock implements gateway.Gateway without moving money. It fabricates
// deterministic references, records every call for test assertions, and
// replays idempotent requests exactly like a real provider: a repeated
// idempotency key returns the original reference without a second effect.
package mock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"escrowpay/internal/gateway"
)

// Call is one recorded gateway invocation.
type Call struct {
	Method         string
	PocketID       string
	AmountKobo     int64
	IdempotencyKey string
}

// Gateway is the mock implementation. The zero value is not usable; use New.
type Gateway struct {
	mu      sync.Mutex
	calls   []Call
	byKey   map[string]string
	funding map[string]gateway.FundingStatus
}

func New() *Gateway {
	return &Gateway{byKey: make(map[string]string), funding: make(map[string]gateway.FundingStatus)}
}

func (g *Gateway) CreateFundingLink(_ context.Context, req gateway.CreateFundingLinkRequest) (gateway.FundingLink, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ref := deriveRef("fund", req.PocketID)
	g.calls = append(g.calls, Call{Method: "CreateFundingLink", PocketID: req.PocketID, AmountKobo: req.AmountKobo})
	return gateway.FundingLink{
		Ref:       ref,
		URL:       fmt.Sprintf("https://sandbox.invalid/pay/%s", ref),
		ExpiresAt: req.ExpiresAt,
	}, nil
}

func (g *Gateway) Payout(_ context.Context, req gateway.PayoutRequest) (string, error) {
	return g.execute("Payout", req.PocketID, req.AmountKobo, req.IdempotencyKey)
}

func (g *Gateway) Refund(_ context.Context, req gateway.RefundRequest) (string, error) {
	return g.execute("Refund", req.PocketID, req.AmountKobo, req.IdempotencyKey)
}

// execute applies the idempotency contract: the first sighting of a key
// records a call and mints a reference; every subsequent sighting returns the
// same reference with no new effect.
func (g *Gateway) execute(method, pocketID string, amountKobo int64, idempotencyKey string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ref, seen := g.byKey[idempotencyKey]; seen {
		return ref, nil
	}
	ref := deriveRef(method, idempotencyKey)
	g.byKey[idempotencyKey] = ref
	g.calls = append(g.calls, Call{Method: method, PocketID: pocketID, AmountKobo: amountKobo, IdempotencyKey: idempotencyKey})
	return ref, nil
}

// SetFundingStatus primes the answer VerifyFunding returns for an order
// reference, standing in for a provider that has (or has not) seen a payment.
func (g *Gateway) SetFundingStatus(orderRef string, status gateway.FundingStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.funding[orderRef] = status
}

// VerifyFunding implements gateway.FundingVerifier: unseeded references report
// unpaid, exactly like a provider with no record of a payment.
func (g *Gateway) VerifyFunding(_ context.Context, orderRef string) (gateway.FundingStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, Call{Method: "VerifyFunding", IdempotencyKey: orderRef})
	return g.funding[orderRef], nil
}

// Calls returns a copy of every effectful invocation, in order.
func (g *Gateway) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Call, len(g.calls))
	copy(out, g.calls)
	return out
}

func deriveRef(kind, seed string) string {
	sum := sha256.Sum256([]byte(kind + ":" + seed))
	return "mock_" + hex.EncodeToString(sum[:8])
}
