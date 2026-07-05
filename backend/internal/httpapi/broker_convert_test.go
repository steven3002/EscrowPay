package httpapi_test

import (
	"context"
	"testing"

	"escrowpay/internal/gateway"
)

// buyerInitiatedP2P is a buyer-created direct pocket: the buyer lists what they
// want to pay for a ₦10,000 item and shares the vendor link. The recipient may
// turn out to be a broker rather than the end seller.
func buyerInitiatedP2P() map[string]any {
	return map[string]any{
		"structure":                 "p2p",
		"creator_role":              "buyer",
		"mode":                      "cooldown",
		"inspection_window_minutes": 1440,
		"delivery_window_minutes":   2880,
		"amount_kobo":               1000000,
		"item_description":          "Nikon Z6 camera",
		"category":                  "electronics",
	}
}

// TestBrokerConversionFlow covers the middleman case: a buyer creates a pocket
// and shares the vendor link; the recipient claims it as a broker, splitting
// the price into a vendor allocation and their commission; a fresh vendor link
// then lets the real seller take the vendor seat. The buyer's total never
// changes and the double-blind visibility holds.
func TestBrokerConversionFlow(t *testing.T) {
	e := newTestEnv(t)
	buyer := e.login(t, "+2348020000002", "Bola Buyer")
	broker := e.login(t, "+2348030000003", "Kunle Middleman")
	vendor := e.login(t, "+2348010000001", "Ada Stores")

	cr := createPocket(t, e, buyer, buyerInitiatedP2P())
	vendorInvite := cr.Tokens["vendor"]
	if vendorInvite == "" {
		t.Fatal("buyer-created pocket must mint a vendor invitation")
	}

	// The broker converts, keeping ₦6,000 for the vendor and ₦4,000 commission.
	status, data := e.reqAs(t, broker, "POST", "/api/p/"+cr.ShortCode+"/claim-broker", vendorInvite, map[string]any{
		"vendor_amount_kobo": 600000,
	})
	if status != 200 {
		t.Fatalf("claim-broker: status %d, body %s", status, data)
	}
	conv := decode[struct {
		Pocket         map[string]any    `json:"pocket"`
		VendorShareURL string            `json:"vendor_share_url"`
		Tokens         map[string]string `json:"tokens"`
	}](t, data)
	if conv.Pocket["structure"] != "brokered" {
		t.Fatalf("structure after conversion = %v, want brokered", conv.Pocket["structure"])
	}
	// The broker sees the full ledger and its split.
	money := conv.Pocket["money"].(map[string]any)
	if money["amount_kobo"].(float64) != 600000 || money["commission_kobo"].(float64) != 400000 {
		t.Fatalf("broker money = %v, want vendor 600000 / commission 400000", money)
	}
	if money["buyer_total_kobo"].(float64) != 1010000 {
		t.Fatalf("buyer total after conversion = %v, want 1010000 unchanged", money["buyer_total_kobo"])
	}

	// The invitation the broker used is dead now; the fresh vendor token works.
	freshVendor := conv.Tokens["vendor"]
	if freshVendor == "" || freshVendor == vendorInvite {
		t.Fatalf("expected a rotated vendor token, got %q (old %q)", freshVendor, vendorInvite)
	}
	if status, _ := e.reqAs(t, vendor, "POST", "/api/p/"+cr.ShortCode+"/claim", vendorInvite, map[string]any{}); status == 200 {
		t.Fatal("the broker's spent invitation must no longer claim the vendor seat")
	}

	// The real vendor claims via the fresh link and accepts. The buyer accepted
	// their own terms at creation and the broker accepted at conversion, so the
	// vendor's acceptance is the last one outstanding and fires transition #1.
	if status, data := e.reqAs(t, vendor, "POST", "/api/p/"+cr.ShortCode+"/claim", freshVendor, map[string]any{}); status != 200 {
		t.Fatalf("vendor claim via fresh link: status %d, body %s", status, data)
	}
	if status, data := e.reqAs(t, vendor, "POST", "/api/p/"+cr.ShortCode+"/accept", freshVendor, map[string]any{}); status != 200 {
		t.Fatalf("vendor accept: status %d, body %s", status, data)
	}

	// The vendor sees only their allocation — never the commission or total.
	_, vdata := e.reqAs(t, vendor, "GET", "/api/p/"+cr.ShortCode, freshVendor, nil)
	vview := decode[map[string]any](t, vdata)
	if vview["state"] != "CREATED" {
		t.Fatalf("state after all three accept = %v, want CREATED", vview["state"])
	}
	vmoney := vview["money"].(map[string]any)
	if vmoney["amount_kobo"].(float64) != 600000 {
		t.Fatalf("vendor allocation = %v, want 600000", vmoney["amount_kobo"])
	}
	if _, leaked := vmoney["commission_kobo"]; leaked {
		t.Fatal("vendor must not see the broker commission")
	}
	if _, leaked := vmoney["buyer_total_kobo"]; leaked {
		t.Fatal("vendor must not see the buyer total")
	}

	// The buyer's view stays a brokered buyer's view: their total only.
	_, bdata := e.reqAs(t, buyer, "GET", "/api/p/"+cr.ShortCode, "", nil)
	bview := decode[map[string]any](t, bdata)
	if bview["state"] != "CREATED" {
		t.Fatalf("buyer state = %v, want CREATED", bview["state"])
	}
	bmoney := bview["money"].(map[string]any)
	if bmoney["buyer_total_kobo"].(float64) != 1010000 {
		t.Fatalf("buyer total = %v, want 1010000", bmoney["buyer_total_kobo"])
	}
	if _, leaked := bmoney["amount_kobo"]; leaked {
		t.Fatal("buyer must not see the vendor allocation in a brokered pocket")
	}
}

func TestBrokerConversionRejections(t *testing.T) {
	e := newTestEnv(t)
	buyer := e.login(t, "+2348020000002", "Bola Buyer")
	broker := e.login(t, "+2348030000003", "Kunle Middleman")

	t.Run("allocation above the item amount is rejected", func(t *testing.T) {
		cr := createPocket(t, e, buyer, buyerInitiatedP2P())
		status, _ := e.reqAs(t, broker, "POST", "/api/p/"+cr.ShortCode+"/claim-broker", cr.Tokens["vendor"], map[string]any{
			"vendor_amount_kobo": 1000000,
		})
		if status != 400 {
			t.Fatalf("full-price allocation status = %d, want 400", status)
		}
	})

	t.Run("zero allocation is rejected", func(t *testing.T) {
		cr := createPocket(t, e, buyer, buyerInitiatedP2P())
		status, _ := e.reqAs(t, broker, "POST", "/api/p/"+cr.ShortCode+"/claim-broker", cr.Tokens["vendor"], map[string]any{
			"vendor_amount_kobo": 0,
		})
		if status != 400 {
			t.Fatalf("zero allocation status = %d, want 400", status)
		}
	})

	t.Run("a vendor-created pocket cannot be brokered by its buyer link", func(t *testing.T) {
		vendor := e.login(t, "+2348010000001", "Ada Stores")
		cr := createPocket(t, e, vendor, vendorInitiatedP2P())
		// The buyer invitation of a vendor-created pocket is not a vendor seat.
		status, _ := e.reqAs(t, broker, "POST", "/api/p/"+cr.ShortCode+"/claim-broker", cr.Tokens["buyer"], map[string]any{
			"vendor_amount_kobo": 600000,
		})
		if status == 200 {
			t.Fatal("only the vendor invitation of a buyer-created pocket may convert")
		}
	})
}

// TestVerifyFundingCreditsPayment proves the pull-side recovery path: when a
// provider reports a CREATED pocket's order paid, verify-funding funds it
// through the same path the webhook uses, without the sandbox shortcut.
func TestVerifyFundingCreditsPayment(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr)

	rec, err := e.store.GetByID(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatalf("load pocket: %v", err)
	}

	// Before the provider sees a payment, verification leaves it CREATED.
	status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/verify-funding", "", nil)
	if status != 200 {
		t.Fatalf("verify (unpaid): status %d, body %s", status, data)
	}
	if decode[map[string]any](t, data)["funded"] != false {
		t.Fatalf("unpaid verify reported funded: %s", data)
	}
	if got := pocketState(t, e, cr.PocketID); got != "CREATED" {
		t.Fatalf("state after unpaid verify = %s, want CREATED", got)
	}

	// The provider now reports the order paid in full.
	e.gateway.SetFundingStatus(rec.FundingLinkRef, gateway.FundingStatus{
		Paid:       true,
		PaymentRef: "PAY-VERIFY-1",
		AmountKobo: rec.Pocket.BuyerTotalKobo(),
	})

	status, data = e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/verify-funding", "", nil)
	if status != 200 {
		t.Fatalf("verify (paid): status %d, body %s", status, data)
	}
	res := decode[map[string]any](t, data)
	if res["funded"] != true || res["state"] != "FUNDED" {
		t.Fatalf("verify (paid) = %s, want funded/FUNDED", data)
	}

	// A second verification is a harmless no-op — the pocket is already funded.
	status, data = e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/verify-funding", "", nil)
	if status != 200 || decode[map[string]any](t, data)["funded"] != true {
		t.Fatalf("repeat verify = %d %s", status, data)
	}
	if got := pocketState(t, e, cr.PocketID); got != "FUNDED" {
		t.Fatalf("state = %s, want FUNDED", got)
	}
}

// TestFeeQuoteAndServerSidePremium proves the premium is platform policy: the
// quote endpoint returns the schedule's value, and creation ignores any
// client-submitted premium in favor of the computed one.
func TestFeeQuoteAndServerSidePremium(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)

	status, data := e.req(t, "GET", "/api/fees?goods_kobo=1000000", "", nil)
	if status != 200 {
		t.Fatalf("fee quote: status %d, body %s", status, data)
	}
	q := decode[map[string]any](t, data)
	if q["premium_kobo"].(float64) != 10000 || q["buyer_total_kobo"].(float64) != 1010000 {
		t.Fatalf("fee quote = %s, want premium 10000 / total 1010000", data)
	}

	// Create with an absurd client premium; the server recomputes ₦100.
	body := vendorInitiatedP2P()
	body["premium_kobo"] = 99999999
	cr := createPocket(t, e, c.vendor, body)
	claimAndAccept(t, e, c.buyer, cr)

	rec, err := e.store.GetByID(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatalf("load pocket: %v", err)
	}
	if rec.Pocket.PremiumKobo != 10000 {
		t.Fatalf("stored premium = %d, want the server-computed 10000", rec.Pocket.PremiumKobo)
	}
	if rec.Pocket.BuyerTotalKobo() != 1010000 {
		t.Fatalf("buyer total = %d, want 1010000", rec.Pocket.BuyerTotalKobo())
	}
}
