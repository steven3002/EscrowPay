package httpapi_test

import (
	"context"
	"sort"
	"testing"
	"time"
)

// brokerInitiated is the standard brokered create request: a broker lists a
// vendor's ₦10,000 item, taking a ₦1,500 commission, with a ₦200 protection fee.
func brokerInitiated() map[string]any {
	return map[string]any{
		"structure":                 "brokered",
		"creator_role":              "broker",
		"mode":                      "cooldown",
		"inspection_window_minutes": 1440,
		"delivery_window_minutes":   2880,
		"amount_kobo":               1000000,
		"commission_kobo":           150000,
		"premium_kobo":              20000,
		"item_description":          "Nikon Z6 camera",
		"category":                  "electronics",
	}
}

func brokerActor(t *testing.T, e *testEnv) *actor {
	t.Helper()
	return e.login(t, "+2348030000003", "Bridge Broker")
}

// acceptBrokered runs the vendor-first acceptance: vendor claims and accepts,
// then the buyer claims and accepts, firing transition #1.
func acceptBrokered(t *testing.T, e *testEnv, c cast, cr createResp) {
	t.Helper()
	vt := cr.Tokens["vendor"]
	if s, d := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/claim", vt, map[string]any{}); s != 200 {
		t.Fatalf("vendor claim: %d %s", s, d)
	}
	if s, d := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{}); s != 200 {
		t.Fatalf("vendor accept: %d %s", s, d)
	}
	bt := cr.Tokens["buyer"]
	if s, d := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", bt, map[string]any{}); s != 200 {
		t.Fatalf("buyer claim: %d %s", s, d)
	}
	if s, d := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{"delivery_address": "14 Marina Road, Lagos"}); s != 200 {
		t.Fatalf("buyer accept: %d %s", s, d)
	}
}

// TestBrokeredVendorFirstAcceptance proves the buyer link is inert until the
// vendor accepts: an early buyer acceptance is rejected, and the same call
// succeeds once the vendor has confirmed.
func TestBrokeredVendorFirstAcceptance(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	broker := brokerActor(t, e)
	cr := createPocket(t, e, broker, brokerInitiated())
	if cr.CounterpartyRole != "" {
		t.Fatalf("brokered counterparty_role = %q, want empty (two counterparties)", cr.CounterpartyRole)
	}

	bt := cr.Tokens["buyer"]
	if s, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", bt, map[string]any{}); s != 200 {
		t.Fatalf("buyer claim status = %d", s)
	}
	// Buyer accepts before the vendor: rejected.
	if s, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{"delivery_address": "14 Marina"}); s != 409 {
		t.Fatalf("early buyer accept status = %d, want 409", s)
	}

	// Vendor accepts, then the buyer's acceptance goes through and fires #1.
	vt := cr.Tokens["vendor"]
	e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/claim", vt, map[string]any{})
	if s, d := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{}); s != 200 {
		t.Fatalf("vendor accept: %d %s", s, d)
	}
	if s, d := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{"delivery_address": "14 Marina"}); s != 200 {
		t.Fatalf("buyer accept after vendor: %d %s", s, d)
	}
	if got := stateOf(t, e, c, cr); got != "CREATED" {
		t.Fatalf("state = %s, want CREATED", got)
	}
}

// TestBrokeredSplitSettlement proves settlement disburses two legs — the vendor
// allocation and the broker commission — each exactly once, even across repeated
// sweeps.
func TestBrokeredSplitSettlement(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	broker := brokerActor(t, e)
	cr := createPocket(t, e, broker, brokerInitiated())
	acceptBrokered(t, e, c, cr)
	if s, d := e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil); s != 200 {
		t.Fatalf("funding: %d %s", s, d)
	}
	code := releaseCodeOf(t, e, c, cr)
	if _, res := enterCode(t, e, c, cr, code); res.State != "DELIVERED_PENDING" {
		t.Fatalf("code entry state = %s", res.State)
	}

	e.clock.advance(1441 * time.Minute)
	if rep := e.tick(t); rep.Settled != 1 {
		t.Fatalf("sweep = %+v, want 1 settled", rep)
	}
	if got := stateOf(t, e, c, cr); got != "SETTLED" {
		t.Fatalf("state = %s, want SETTLED", got)
	}

	// Two confirmed payout legs: vendor 1,000,000 and broker 150,000.
	rows, err := testPool.Query(context.Background(),
		`SELECT beneficiary_role, amount_kobo, status FROM settlements WHERE pocket_id = $1 AND direction = 'payout' ORDER BY beneficiary_role`, cr.PocketID)
	if err != nil {
		t.Fatal(err)
	}
	type leg struct {
		role   string
		amount int64
		status string
	}
	var legs []leg
	for rows.Next() {
		var l leg
		if err := rows.Scan(&l.role, &l.amount, &l.status); err != nil {
			t.Fatal(err)
		}
		legs = append(legs, l)
	}
	rows.Close()
	sort.Slice(legs, func(i, j int) bool { return legs[i].role < legs[j].role })
	want := []leg{{"broker", 150000, "confirmed"}, {"vendor", 1000000, "confirmed"}}
	if len(legs) != 2 || legs[0] != want[0] || legs[1] != want[1] {
		t.Fatalf("payout legs = %+v, want %+v", legs, want)
	}

	// Exactly two gateway payouts, and repeated sweeps add none.
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 2 {
		t.Fatalf("payout calls = %d, want 2", n)
	}
	for i := 0; i < 3; i++ {
		e.tick(t)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 2 {
		t.Fatalf("payout calls after repeated sweeps = %d, want 2", n)
	}
}

// TestBrokeredDoubleBlindViews proves the double-blind serialization: the vendor
// never sees the commission or buyer total, and the buyer's view has exactly the
// same money shape as a p2p buyer view (no vendor split, no vendor identity).
func TestBrokeredDoubleBlindViews(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	broker := brokerActor(t, e)
	cr := createPocket(t, e, broker, brokerInitiated())
	acceptBrokered(t, e, c, cr)
	e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)

	// Vendor view: allocation only.
	_, vd := e.reqAs(t, c.vendor, "GET", "/api/p/"+cr.ShortCode, "", nil)
	vendorMoney := decode[map[string]any](t, vd)["money"].(map[string]any)
	if _, ok := vendorMoney["amount_kobo"]; !ok {
		t.Fatal("vendor must see amount_kobo")
	}
	for _, hidden := range []string{"commission_kobo", "buyer_total_kobo", "premium_kobo"} {
		if _, ok := vendorMoney[hidden]; ok {
			t.Fatalf("vendor leaked %s", hidden)
		}
	}

	// Buyer view money shape equals the p2p buyer shape: exactly currency +
	// buyer_total_kobo, nothing revealing the split.
	_, bd := e.reqAs(t, c.buyer, "GET", "/api/p/"+cr.ShortCode, "", nil)
	buyerView := decode[map[string]any](t, bd)
	buyerMoney := buyerView["money"].(map[string]any)
	gotKeys := make([]string, 0, len(buyerMoney))
	for k := range buyerMoney {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	want := []string{"buyer_total_kobo", "currency"}
	if len(gotKeys) != len(want) || gotKeys[0] != want[0] || gotKeys[1] != want[1] {
		t.Fatalf("buyer money keys = %v, want %v (p2p shape)", gotKeys, want)
	}
	// The buyer transacts with the broker storefront, never the vendor identity.
	if cp, ok := buyerView["counterparty"].(map[string]any); ok && cp["role"] == "vendor" {
		t.Fatal("buyer view exposed the vendor as counterparty")
	}
}

// TestBrokeredDisputeRefund proves a brokered refund is a single buyer-total leg
// with no payout legs.
func TestBrokeredDisputeRefund(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	broker := brokerActor(t, e)
	cr := createPocket(t, e, broker, brokerInitiated())
	acceptBrokered(t, e, c, cr)
	e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)
	code := releaseCodeOf(t, e, c, cr)
	enterCode(t, e, c, cr, code) // -> DELIVERED_PENDING
	e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil)

	admin := e.loginAdmin(t)
	if s, d := e.reqAs(t, admin, "POST", "/api/admin/pockets/"+cr.PocketID+"/force-refund", "", nil); s != 200 {
		t.Fatalf("force-refund: %d %s", s, d)
	}
	if got := stateOf(t, e, c, cr); got != "REFUNDED" {
		t.Fatalf("state = %s, want REFUNDED", got)
	}

	var role string
	var amount int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT beneficiary_role, amount_kobo FROM settlements WHERE pocket_id = $1 AND direction = 'refund'`, cr.PocketID).
		Scan(&role, &amount); err != nil {
		t.Fatal(err)
	}
	if role != "buyer" || amount != 1170000 {
		t.Fatalf("refund leg = %s/%d, want buyer/1170000 (full buyer total)", role, amount)
	}
	var payouts int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM settlements WHERE pocket_id = $1 AND direction = 'payout'`, cr.PocketID).Scan(&payouts); err != nil {
		t.Fatal(err)
	}
	if payouts != 0 {
		t.Fatalf("payout legs on refund = %d, want 0", payouts)
	}
}
