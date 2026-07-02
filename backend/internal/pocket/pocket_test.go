package pocket

import (
	"errors"
	"testing"
	"time"
)

func validP2PSpec() Spec {
	return Spec{
		Structure:        StructureP2P,
		CreatorRole:      RoleVendor,
		Mode:             ModeCooldown,
		InspectionWindow: 24 * time.Hour,
		DeliveryWindow:   48 * time.Hour,
		AmountKobo:       1_000_000,
		PremiumKobo:      20_000,
		FundingTTL:       72 * time.Hour,
	}
}

// TestNewCreatesPocketWithFundingLink asserts edge #1.
func TestNewCreatesPocketWithFundingLink(t *testing.T) {
	out, err := New(testNow, validP2PSpec())
	if err != nil {
		t.Fatal(err)
	}
	if out.Pocket.State != StateCreated {
		t.Fatalf("state = %s", out.Pocket.State)
	}
	if !containsEffect[CreateFundingLink](out.Effects) {
		t.Fatal("expected CreateFundingLink effect")
	}
	if !out.Pocket.FundingExpiresAt.Equal(testNow.Add(72 * time.Hour)) {
		t.Fatalf("funding expiry = %v", out.Pocket.FundingExpiresAt)
	}
	if out.Pocket.GracePeriod != DefaultGracePeriod || out.Pocket.EvidenceCaptureWindow != DefaultEvidenceCaptureWindow {
		t.Fatal("policy defaults not applied")
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Spec)
	}{
		{"zero amount", func(s *Spec) { s.AmountKobo = 0 }},
		{"p2p with commission", func(s *Spec) { s.CommissionKobo = 5_000 }},
		{"instant with window", func(s *Spec) { s.Mode = ModeInstant; s.InspectionWindow = time.Hour }},
		{"cooldown without window", func(s *Spec) { s.Mode = ModeCooldown; s.InspectionWindow = 0 }},
		{"zero delivery window", func(s *Spec) { s.DeliveryWindow = 0 }},
		{"zero funding ttl", func(s *Spec) { s.FundingTTL = 0 }},
		{"p2p creator broker", func(s *Spec) { s.CreatorRole = RoleBroker }},
		{"unknown structure", func(s *Spec) { s.Structure = "syndicate" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validP2PSpec()
			tc.mut(&s)
			if _, err := New(testNow, s); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("err = %v, want ErrInvalidSpec", err)
			}
		})
	}
}

func TestP2PPayoutLegs(t *testing.T) {
	out, err := New(testNow, validP2PSpec())
	if err != nil {
		t.Fatal(err)
	}
	legs := out.Pocket.PayoutLegs()
	if len(legs) != 1 || legs[0].Role != RoleVendor || legs[0].AmountKobo != 1_000_000 {
		t.Fatalf("p2p legs = %+v", legs)
	}
	if out.Pocket.BuyerTotalKobo() != 1_020_000 {
		t.Fatalf("buyer total = %d", out.Pocket.BuyerTotalKobo())
	}
}

func TestBrokeredSpecAndSplitLegs(t *testing.T) {
	s := Spec{
		Structure:        StructureBrokered,
		CreatorRole:      RoleBroker,
		Mode:             ModeCooldown,
		InspectionWindow: 24 * time.Hour,
		DeliveryWindow:   48 * time.Hour,
		AmountKobo:       1_000_000,
		CommissionKobo:   200_000,
		PremiumKobo:      20_000,
		FundingTTL:       72 * time.Hour,
	}
	out, err := New(testNow, s)
	if err != nil {
		t.Fatal(err)
	}
	legs := out.Pocket.PayoutLegs()
	if len(legs) != 2 {
		t.Fatalf("want 2 legs, got %d: %+v", len(legs), legs)
	}
	if legs[0].Role != RoleVendor || legs[0].AmountKobo != 1_000_000 {
		t.Fatalf("vendor leg = %+v", legs[0])
	}
	if legs[1].Role != RoleBroker || legs[1].AmountKobo != 200_000 {
		t.Fatalf("broker leg = %+v", legs[1])
	}
	if out.Pocket.BuyerTotalKobo() != 1_220_000 {
		t.Fatalf("buyer total = %d", out.Pocket.BuyerTotalKobo())
	}
}

// TestInstantModeSettlesImmediately covers the instant path: handoff yields a
// delivered pocket that is due for settlement at the same instant, with no
// evidence prompt (delivery-only protection).
func TestInstantModeSettlesImmediately(t *testing.T) {
	p := mkPocket(StateFunded, func(p *Pocket) {
		p.Mode = ModeInstant
		p.InspectionWindow = 0
	})
	out, err := p.Transition(testNow, Event{Kind: EvCodeAccepted})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pocket.State != StateDeliveredPending {
		t.Fatalf("state = %s", out.Pocket.State)
	}
	if !out.Pocket.DueForSettlement(testNow) {
		t.Fatal("instant mode must be due for settlement immediately")
	}
	if containsEffect[PromptEvidence](out.Effects) {
		t.Fatal("instant mode must not prompt for evidence")
	}
}

// TestCooldownModeDefersSettlement covers the cooldown path: the pocket is not
// due until the window elapses, and an evidence prompt is emitted.
func TestCooldownModeDefersSettlement(t *testing.T) {
	p := mkPocket(StateFunded, nil)
	out, err := p.Transition(testNow, Event{Kind: EvCodeAccepted})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pocket.DueForSettlement(testNow) {
		t.Fatal("cooldown must not be due immediately")
	}
	if !out.Pocket.DueForSettlement(testNow.Add(24 * time.Hour)) {
		t.Fatal("cooldown must be due after the window")
	}
	if !containsEffect[PromptEvidence](out.Effects) {
		t.Fatal("cooldown must prompt for evidence")
	}
}

// TestCodeLockout: five wrong codes lock; a correct code afterward is rejected.
func TestCodeLockout(t *testing.T) {
	p := mkPocket(StateFunded, nil)

	for i := 1; i <= 4; i++ {
		out, err := p.Transition(testNow, Event{Kind: EvCodeRejected})
		if err != nil {
			t.Fatalf("rejection %d: %v", i, err)
		}
		p = out.Pocket
		if p.CodeLocked {
			t.Fatalf("locked prematurely after %d attempts", i)
		}
	}

	// A correct code before lockout still works.
	if _, err := p.Transition(testNow, Event{Kind: EvCodeAccepted}); err != nil {
		t.Fatalf("accept before lockout: %v", err)
	}

	// Fifth failure locks and notifies both parties.
	out, err := p.Transition(testNow, Event{Kind: EvCodeRejected})
	if err != nil {
		t.Fatal(err)
	}
	p = out.Pocket
	if !p.CodeLocked {
		t.Fatal("expected lock after fifth failure")
	}
	if !containsEffect[Notify](out.Effects) {
		t.Fatal("expected lock notifications")
	}

	// A correct code after lockout is rejected.
	if _, err := p.Transition(testNow, Event{Kind: EvCodeAccepted}); !errors.Is(err, ErrCodeLocked) {
		t.Fatalf("accept after lockout: err = %v, want ErrCodeLocked", err)
	}
}
