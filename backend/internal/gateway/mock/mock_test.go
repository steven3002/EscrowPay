package mock

import (
	"context"
	"testing"
	"time"

	"escrowpay/internal/gateway"
)

func TestFundingLinkIsDeterministicPerPocket(t *testing.T) {
	g := New()
	req := gateway.CreateFundingLinkRequest{PocketID: "pocket-1", AmountKobo: 500000, ExpiresAt: time.Now().Add(time.Hour)}

	a, err := g.CreateFundingLink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.CreateFundingLink(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref != b.Ref {
		t.Fatalf("expected stable ref for same pocket, got %q and %q", a.Ref, b.Ref)
	}
}

func TestPayoutIdempotencyReplay(t *testing.T) {
	g := New()
	req := gateway.PayoutRequest{PocketID: "pocket-1", BeneficiaryRole: "vendor", AmountKobo: 1000000, IdempotencyKey: "pocket-1:vendor:payout"}

	first, err := g.Payout(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.Payout(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("idempotent replay must return the original ref: %q vs %q", first, second)
	}

	payouts := 0
	for _, c := range g.Calls() {
		if c.Method == "Payout" {
			payouts++
		}
	}
	if payouts != 1 {
		t.Fatalf("expected exactly one effectful payout, got %d", payouts)
	}
}

func TestDistinctLegsProduceDistinctRefs(t *testing.T) {
	g := New()
	vendor, _ := g.Payout(context.Background(), gateway.PayoutRequest{PocketID: "p", BeneficiaryRole: "vendor", AmountKobo: 1000000, IdempotencyKey: "p:vendor:payout"})
	broker, _ := g.Payout(context.Background(), gateway.PayoutRequest{PocketID: "p", BeneficiaryRole: "broker", AmountKobo: 200000, IdempotencyKey: "p:broker:payout"})
	if vendor == broker {
		t.Fatalf("separate settlement legs must not share a gateway ref")
	}
	if len(g.Calls()) != 2 {
		t.Fatalf("expected two recorded calls, got %d", len(g.Calls()))
	}
}
