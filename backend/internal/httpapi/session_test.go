package httpapi_test

import (
	"testing"
)

// TestClaimedSeatRejectsBareLink proves a share link stops being a credential
// once its seat is claimed: only the bound account's session may act on or
// even view the seat afterwards.
func TestClaimedSeatRejectsBareLink(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	claimAndAccept(t, e, c.buyer, cr)

	buyerToken := cr.Tokens["buyer"]

	// Before claiming, the invitation token rendered the terms; after the
	// claim, an anonymous holder of the same link gets nothing.
	if status, _ := e.req(t, "GET", "/api/p/"+cr.ShortCode, buyerToken, nil); status != 401 {
		t.Fatalf("anonymous view of claimed seat = %d, want 401", status)
	}

	// A different signed-in account holding the leaked link is refused.
	mallory := e.login(t, "+2348099999999", "Mallory")
	if status, _ := e.reqAs(t, mallory, "GET", "/api/p/"+cr.ShortCode, buyerToken, nil); status != 403 {
		t.Fatalf("foreign account view of claimed seat = %d, want 403", status)
	}

	// The seat's owner needs no token at all: the session identifies them.
	if status, _ := e.reqAs(t, c.buyer, "GET", "/api/p/"+cr.ShortCode, "", nil); status != 200 {
		t.Fatal("owner session denied their own pocket")
	}

	// The leaked link cannot reach the Release Code either.
	e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil)
	if status, _ := e.req(t, "GET", "/api/pockets/"+cr.PocketID+"/release-code", buyerToken, nil); status != 401 {
		t.Fatalf("anonymous release-code with leaked link = %d, want 401", status)
	}
	if status, _ := e.reqAs(t, mallory, "GET", "/api/pockets/"+cr.PocketID+"/release-code", buyerToken, nil); status != 403 {
		t.Fatalf("foreign account release-code with leaked link = %d, want 403", status)
	}
}

// TestClaimBindingRules covers the claim endpoint's account rules: a session is
// required, re-claim by the same account is idempotent, a second account is
// rejected, and one account cannot hold two roles in a pocket.
func TestClaimBindingRules(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	buyerToken := cr.Tokens["buyer"]

	// Claiming needs an account.
	if status, _ := e.req(t, "POST", "/api/p/"+cr.ShortCode+"/claim", buyerToken, map[string]any{}); status != 401 {
		t.Fatalf("anonymous claim = %d, want 401", status)
	}
	// The invitee claims; doing it twice is harmless.
	if status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", buyerToken, map[string]any{}); status != 200 {
		t.Fatal("buyer claim failed")
	}
	if status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/claim", buyerToken, map[string]any{}); status != 200 {
		t.Fatal("idempotent re-claim failed")
	}
	// A different account cannot take the bound seat.
	mallory := e.login(t, "+2348099999999", "Mallory")
	if status, _ := e.reqAs(t, mallory, "POST", "/api/p/"+cr.ShortCode+"/claim", buyerToken, map[string]any{}); status != 403 {
		t.Fatalf("foreign claim of bound seat = %d, want 403", status)
	}

	// The vendor (creator) cannot also claim the buyer seat of their own
	// pocket: the parties of an escrow must be distinct.
	cr2 := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	if status, _ := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr2.ShortCode+"/claim", cr2.Tokens["buyer"], map[string]any{}); status != 409 {
		t.Fatalf("self-claim of second role = %d, want 409", status)
	}
}

// TestDashboardListsAllRolesWithScopedMoney proves the dashboard feed: one
// account sees every pocket it participates in with its per-pocket role, and
// each row's money fields obey that role's visibility.
func TestDashboardListsAllRolesWithScopedMoney(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)

	// Ada is the vendor of one pocket and the buyer of another.
	asVendor := createPocket(t, e, c.vendor, vendorInitiatedP2P())
	buyerInit := vendorInitiatedP2P()
	buyerInit["creator_role"] = "buyer"
	asBuyer := createPocket(t, e, c.vendor, buyerInit)

	// An account with no session cannot read a dashboard.
	if status, _ := e.req(t, "GET", "/api/me/pockets", "", nil); status != 401 {
		t.Fatal("anonymous dashboard should 401")
	}

	_, data := e.reqAs(t, c.vendor, "GET", "/api/me/pockets", "", nil)
	feed := decode[struct {
		Pockets []struct {
			ShortCode string         `json:"short_code"`
			Role      string         `json:"role"`
			State     string         `json:"state"`
			Active    bool           `json:"active"`
			Money     map[string]any `json:"money"`
		} `json:"pockets"`
	}](t, data)
	if len(feed.Pockets) != 2 {
		t.Fatalf("dashboard rows = %d, want 2", len(feed.Pockets))
	}
	byCode := map[string]int{}
	for i, p := range feed.Pockets {
		byCode[p.ShortCode] = i
	}
	v := feed.Pockets[byCode[asVendor.ShortCode]]
	if v.Role != "vendor" || !v.Active {
		t.Fatalf("vendor row = %+v", v)
	}
	if _, ok := v.Money["amount_kobo"]; !ok {
		t.Fatal("vendor row must show the allocation")
	}
	if _, ok := v.Money["buyer_total_kobo"]; ok {
		t.Fatal("vendor row leaked the buyer total")
	}
	b := feed.Pockets[byCode[asBuyer.ShortCode]]
	if b.Role != "buyer" {
		t.Fatalf("buyer row = %+v", b)
	}
	if _, ok := b.Money["buyer_total_kobo"]; !ok {
		t.Fatal("buyer row must show the buyer total")
	}
	if _, ok := b.Money["amount_kobo"]; ok {
		t.Fatal("buyer row leaked the vendor allocation")
	}

	// The other account has an empty dashboard: participation, not existence,
	// scopes the feed.
	_, other := e.reqAs(t, c.buyer, "GET", "/api/me/pockets", "", nil)
	if feed := decode[map[string]any](t, other); feed["pockets"] != nil {
		if rows, ok := feed["pockets"].([]any); ok && len(rows) != 0 {
			t.Fatalf("uninvolved account sees %d pockets, want 0", len(rows))
		}
	}
}

// TestLogoutRevokesSession proves logout invalidates the cookie server-side.
func TestLogoutRevokesSession(t *testing.T) {
	e := newTestEnv(t)
	u := e.login(t, "+2348010000001", "Ada Stores")

	if status, _ := e.reqAs(t, u, "GET", "/api/auth/me", "", nil); status != 200 {
		t.Fatal("live session refused")
	}
	if status, _ := e.reqAs(t, u, "POST", "/api/auth/logout", "", nil); status != 204 {
		t.Fatal("logout failed")
	}
	if status, _ := e.reqAs(t, u, "GET", "/api/auth/me", "", nil); status != 401 {
		t.Fatal("revoked session still authenticates")
	}
}

// TestAuthRateLimit proves the credential endpoint budget: repeated demo
// logins from one client are cut off with 429 once the burst is spent.
func TestAuthRateLimit(t *testing.T) {
	e := newEnv(t, envOptions{sandbox: true, rateLimit: true})

	got429 := false
	for i := 0; i < 12; i++ {
		status, _ := e.req(t, "POST", "/api/auth/demo", "", map[string]any{
			"phone": "+2348010000001", "display_name": "Ada",
		})
		if status == 429 {
			got429 = true
			break
		}
		if status != 200 {
			t.Fatalf("login %d status = %d", i+1, status)
		}
	}
	if !got429 {
		t.Fatal("credential endpoint never rate limited")
	}
}
