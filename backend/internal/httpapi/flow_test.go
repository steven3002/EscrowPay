package httpapi_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// createResp mirrors the create endpoint's JSON.
type createResp struct {
	PocketID         string            `json:"pocket_id"`
	ShortCode        string            `json:"short_code"`
	CreatorRole      string            `json:"creator_role"`
	CounterpartyRole string            `json:"counterparty_role"`
	ShareURL         string            `json:"share_url"`
	Tokens           map[string]string `json:"tokens"`
}

// cast is the standard set of signed-in accounts driving a pocket's lifecycle.
type cast struct {
	vendor *actor
	buyer  *actor
}

func stdCast(t *testing.T, e *testEnv) cast {
	t.Helper()
	return cast{
		vendor: e.login(t, "+2348010000001", "Ada Stores"),
		buyer:  e.login(t, "+2348020000002", "Bola Buyer"),
	}
}

// vendorInitiatedP2P is the standard create request used across tests: a vendor
// lists a ₦10,000 item with a ₦200 premium and a 24h inspection window.
func vendorInitiatedP2P() map[string]any {
	return map[string]any{
		"structure":                 "p2p",
		"creator_role":              "vendor",
		"mode":                      "cooldown",
		"inspection_window_minutes": 1440,
		"delivery_window_minutes":   2880,
		"amount_kobo":               1000000,
		"premium_kobo":              20000,
		"item_description":          "Nikon Z6 camera",
		"category":                  "electronics",
	}
}

// createPocket runs the create request as the given signed-in creator and
// returns the parsed response.
func createPocket(t *testing.T, e *testEnv, creator *actor, body map[string]any) createResp {
	t.Helper()
	status, data := e.reqAs(t, creator, "POST", "/api/pockets", "", body)
	if status != 201 {
		t.Fatalf("create: status %d, body %s", status, data)
	}
	return decode[createResp](t, data)
}

// claimAndAccept has the signed-in buyer claim their seat via the share link's
// token and accept, supplying a delivery address. It returns after transition
// #1 has fired.
func claimAndAccept(t *testing.T, e *testEnv, buyer *actor, cr createResp) {
	t.Helper()
	buyerToken := cr.Tokens["buyer"]
	if status, data := e.reqAs(t, buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", buyerToken, map[string]any{}); status != 200 {
		t.Fatalf("claim: status %d, body %s", status, data)
	}
	if status, data := e.reqAs(t, buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", buyerToken, map[string]any{
		"delivery_address": "14 Marina Road, Lagos",
	}); status != 200 {
		t.Fatalf("accept: status %d, body %s", status, data)
	}
}

func TestHappyPathToFundedAndCodeFetch(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())

	if cr.CounterpartyRole != "buyer" {
		t.Fatalf("counterparty role = %q, want buyer", cr.CounterpartyRole)
	}
	if cr.Tokens["buyer"] == "" || cr.Tokens["vendor"] == "" {
		t.Fatalf("expected both role tokens, got %+v", cr.Tokens)
	}

	claimAndAccept(t, e, c.buyer, cr)

	// After acceptance the pocket is CREATED with a funding link visible to buyer.
	status, data := e.reqAs(t, c.buyer, "GET", "/api/p/"+cr.ShortCode, "", nil)
	if status != 200 {
		t.Fatalf("buyer view: status %d, body %s", status, data)
	}
	view := decode[map[string]any](t, data)
	if view["state"] != "CREATED" {
		t.Fatalf("state after accept = %v, want CREATED", view["state"])
	}
	if view["funding_url"] == nil || view["funding_url"] == "" {
		t.Fatal("buyer should see a funding_url in CREATED")
	}

	// Simulate funding (transition #2).
	status, data = e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)
	if status != 200 {
		t.Fatalf("simulate-funding: status %d, body %s", status, data)
	}
	if decode[map[string]any](t, data)["state"] != "FUNDED" {
		t.Fatalf("state after funding = %v, want FUNDED", decode[map[string]any](t, data)["state"])
	}

	// Buyer fetches the plaintext Release Code.
	status, data = e.reqAs(t, c.buyer, "GET", "/api/pockets/"+cr.PocketID+"/release-code", "", nil)
	if status != 200 {
		t.Fatalf("release-code: status %d, body %s", status, data)
	}
	code := decode[struct {
		ReleaseCode string `json:"release_code"`
	}](t, data).ReleaseCode
	if len(code) != 4 {
		t.Fatalf("release code = %q, want 4 digits", code)
	}

	// A second fetch returns the same code (encrypted-at-rest, not regenerated).
	_, data2 := e.reqAs(t, c.buyer, "GET", "/api/pockets/"+cr.PocketID+"/release-code", "", nil)
	if got := decode[struct {
		ReleaseCode string `json:"release_code"`
	}](t, data2).ReleaseCode; got != code {
		t.Fatalf("release code changed between fetches: %q vs %q", code, got)
	}

	// Audit: exactly two state changes (created, funded) => two events.
	n, err := e.store.CountEvents(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("event count = %d, want 2 (created, funding_confirmed)", n)
	}

	// The gateway saw a funding-link creation and no premature payout.
	calls := e.gateway.Calls()
	if len(calls) != 1 || calls[0].Method != "CreateFundingLink" {
		t.Fatalf("gateway calls = %+v, want one CreateFundingLink", calls)
	}

	// The persisted delivery window drove the funding-time deadline: 2880 minutes
	// stored at creation, delivery_deadline = funding time + that window.
	var windowMin int
	var deadline *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT delivery_window_minutes, delivery_deadline FROM pockets WHERE id = $1`, cr.PocketID).
		Scan(&windowMin, &deadline); err != nil {
		t.Fatal(err)
	}
	if windowMin != 2880 {
		t.Fatalf("delivery_window_minutes = %d, want 2880", windowMin)
	}
	if deadline == nil || !deadline.Equal(fixedNow.Add(2880*time.Minute)) {
		t.Fatalf("delivery_deadline = %v, want %v", deadline, fixedNow.Add(2880*time.Minute))
	}
}

func TestReleaseCodeNeverLeaksToVendorOrLogs(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr)
	e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)

	// The buyer can read the code.
	_, data := e.reqAs(t, c.buyer, "GET", "/api/pockets/"+cr.PocketID+"/release-code", "", nil)
	code := decode[struct {
		ReleaseCode string `json:"release_code"`
	}](t, data).ReleaseCode
	if len(code) != 4 {
		t.Fatalf("release code = %q", code)
	}

	// The vendor cannot: the buyer-only endpoint is forbidden to them.
	if status, _ := e.reqAs(t, c.vendor, "GET", "/api/pockets/"+cr.PocketID+"/release-code", "", nil); status != 403 {
		t.Fatalf("vendor release-code status = %d, want 403", status)
	}

	// The code appears in no vendor-facing response body.
	_, vendorView := e.reqAs(t, c.vendor, "GET", "/api/p/"+cr.ShortCode, "", nil)
	if strings.Contains(string(vendorView), code) || strings.Contains(string(vendorView), "release_code") {
		t.Fatalf("vendor view leaked the release code: %s", vendorView)
	}
	admin := e.loginAdmin(t)
	_, adminView := e.reqAs(t, admin, "GET", "/api/admin/pockets/"+cr.PocketID, "", nil)
	if strings.Contains(string(adminView), code) {
		t.Fatalf("admin view leaked the release code: %s", adminView)
	}

	// The code appears in no log line.
	if logs := e.logs.String(); strings.Contains(logs, code) {
		t.Fatalf("release code leaked to logs: %s", logs)
	}
}

func TestRoleScopedMoneyVisibility(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr)
	e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)

	// Buyer sees the total they paid, not the vendor's allocation or premium.
	_, buyerData := e.reqAs(t, c.buyer, "GET", "/api/p/"+cr.ShortCode, "", nil)
	buyerMoney := decode[map[string]any](t, buyerData)["money"].(map[string]any)
	if _, ok := buyerMoney["buyer_total_kobo"]; !ok {
		t.Fatal("buyer must see buyer_total_kobo")
	}
	if _, ok := buyerMoney["amount_kobo"]; ok {
		t.Fatal("buyer must not see the vendor allocation")
	}

	// Vendor sees their allocation, never the buyer total or premium.
	_, vendorData := e.reqAs(t, c.vendor, "GET", "/api/p/"+cr.ShortCode, "", nil)
	vendorView := decode[map[string]any](t, vendorData)
	vendorMoney := vendorView["money"].(map[string]any)
	if _, ok := vendorMoney["amount_kobo"]; !ok {
		t.Fatal("vendor must see amount_kobo")
	}
	for _, hidden := range []string{"buyer_total_kobo", "premium_kobo"} {
		if _, ok := vendorMoney[hidden]; ok {
			t.Fatalf("vendor must not see %s", hidden)
		}
	}

	// The vendor sees the delivery address once funded (they must ship).
	cp, ok := vendorView["counterparty"].(map[string]any)
	if !ok || cp["delivery_address"] != "14 Marina Road, Lagos" {
		t.Fatalf("vendor should see delivery address after funding, got %v", vendorView["counterparty"])
	}
}

func TestDoubleAcceptRejected(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr) // fires #1

	// A second accept on an already-created pocket is rejected.
	status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{})
	if status != 409 {
		t.Fatalf("second accept status = %d, want 409", status)
	}
}

func TestConcurrentAcceptLeavesOneWinner(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())

	// Buyer claims first, then two accept requests race.
	if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", cr.Tokens["buyer"], map[string]any{}); status != 200 {
		t.Fatalf("claim: %d %s", status, data)
	}

	const racers = 2
	var wg sync.WaitGroup
	statuses := make([]int, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/accept", "", map[string]any{})
			statuses[idx] = status
		}(i)
	}
	wg.Wait()

	wins, losses := 0, 0
	for _, s := range statuses {
		switch s {
		case 200:
			wins++
		case 409:
			losses++
		default:
			t.Fatalf("unexpected accept status %d", s)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("concurrent accept: wins=%d losses=%d, want 1/1", wins, losses)
	}

	// Exactly one state change (draft -> CREATED) was recorded.
	n, err := e.store.CountEvents(context.Background(), cr.PocketID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("event count = %d, want 1", n)
	}
}

func TestCancelDraftAndCreatedAndFunded(t *testing.T) {
	t.Run("draft", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
		// Vendor cancels the still-unaccepted draft.
		status, data := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/cancel", "", nil)
		if status != 200 {
			t.Fatalf("cancel draft: %d %s", status, data)
		}
		if decode[map[string]any](t, data)["state"] != "CANCELLED" {
			t.Fatalf("state = %v, want CANCELLED", decode[map[string]any](t, data)["state"])
		}
	})

	t.Run("created (#4)", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
		claimAndAccept(t, e, c.buyer, cr) // -> CREATED
		status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/cancel", "", nil)
		if status != 200 {
			t.Fatalf("cancel created: %d %s", status, data)
		}
		if decode[map[string]any](t, data)["state"] != "CANCELLED" {
			t.Fatalf("state = %v, want CANCELLED", decode[map[string]any](t, data)["state"])
		}
	})

	t.Run("funded (#5 vendor refund)", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
		claimAndAccept(t, e, c.buyer, cr)
		e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)

		// A buyer may not cancel a funded pocket; only the vendor triggers #5.
		if status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/cancel", "", nil); status != 403 {
			t.Fatalf("buyer cancel of funded pocket status = %d, want 403", status)
		}

		status, data := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/cancel", "", nil)
		if status != 200 {
			t.Fatalf("vendor cancel funded: %d %s", status, data)
		}
		if decode[map[string]any](t, data)["state"] != "REFUNDED" {
			t.Fatalf("state = %v, want REFUNDED", decode[map[string]any](t, data)["state"])
		}

		// The refund executed exactly one leg for the full buyer total.
		var direction, role string
		var amount int64
		err := testPool.QueryRow(context.Background(),
			`SELECT direction, beneficiary_role, amount_kobo FROM settlements WHERE pocket_id = $1`, cr.PocketID).
			Scan(&direction, &role, &amount)
		if err != nil {
			t.Fatalf("settlement lookup: %v", err)
		}
		if direction != "refund" || role != "buyer" || amount != 1010000 {
			t.Fatalf("settlement = %s/%s/%d, want refund/buyer/1010000", direction, role, amount)
		}
	})
}

func TestUnauthorizedAndForbidden(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())

	// No token and no session.
	if status, _ := e.req(t, "GET", "/api/p/"+cr.ShortCode, "", nil); status != 401 {
		t.Fatalf("missing credential status = %d, want 401", status)
	}
	// A token minted for a different pocket must not authorize this one.
	foreign, _, err := e.minter.Mint("00000000-0000-0000-0000-000000000000", "buyer")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := e.req(t, "GET", "/api/p/"+cr.ShortCode, foreign, nil); status != 403 {
		t.Fatalf("foreign token status = %d, want 403", status)
	}
	// Unknown short code is a 404.
	if status, _ := e.reqAs(t, c.buyer, "GET", "/api/p/"+fmt.Sprintf("nope%d", 1), cr.Tokens["buyer"], nil); status != 404 {
		t.Fatalf("unknown short code status = %d, want 404", status)
	}
}
