package httpapi_test

import "testing"

// pendingInvites reads the role-scoped pending_invites list from a decoded
// pocket view. The field is omitempty, so an absent list reads as none.
func pendingInvites(t *testing.T, view map[string]any) []string {
	t.Helper()
	raw, ok := view["pending_invites"]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("pending_invites = %v, want a JSON array", raw)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(string))
	}
	return out
}

func assertRoles(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pending_invites = %v, want %v", got, want)
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r] = true
	}
	for _, r := range want {
		if !seen[r] {
			t.Fatalf("pending_invites = %v, want it to contain %q", got, r)
		}
	}
}

func viewAs(t *testing.T, e *testEnv, ac *actor, shortCode string) map[string]any {
	t.Helper()
	_, data := e.reqAs(t, ac, "GET", "/api/p/"+shortCode, "", nil)
	return decode[map[string]any](t, data)
}

// TestPendingInvitesVisibility pins the "copy the link again" signal: while a
// counterparty seat is unclaimed the view reports it as pending (so the sharer
// can re-offer its link), it clears once the seat is taken, and a brokered
// buyer never learns the hidden vendor seat exists.
func TestPendingInvitesVisibility(t *testing.T) {
	e := newTestEnv(t)
	buyer := e.login(t, "+2348020000002", "Bola Buyer")
	broker := e.login(t, "+2348030000003", "Kunle Middleman")
	vendor := e.login(t, "+2348010000001", "Ada Stores")

	// A buyer-created p2p draft: the vendor seat is open, so the buyer's own
	// view lists it as pending — the link they must forward.
	cr := createPocket(t, e, buyer, buyerInitiatedP2P())
	assertRoles(t, pendingInvites(t, viewAs(t, e, buyer, cr.ShortCode)), "vendor")

	// The recipient takes it as a broker, which rotates the vendor seat's link.
	_, cbData := e.reqAs(t, broker, "POST", "/api/p/"+cr.ShortCode+"/claim-broker", cr.Tokens["vendor"], map[string]any{
		"vendor_amount_kobo": 600000,
	})
	conv := decode[struct {
		Tokens map[string]string `json:"tokens"`
	}](t, cbData)

	// The fresh vendor seat is now the only open one: the broker's view lists
	// vendor (their link to forward), and the brokered buyer's view lists
	// nothing — the vendor seat must stay hidden from them.
	assertRoles(t, pendingInvites(t, viewAs(t, e, broker, cr.ShortCode)), "vendor")
	if got := pendingInvites(t, viewAs(t, e, buyer, cr.ShortCode)); len(got) != 0 {
		t.Fatalf("brokered buyer pending_invites = %v, want none (vendor seat must stay hidden)", got)
	}

	// Once the real vendor claims via the fresh link, no seat is open: the
	// broker's view clears, so the re-copy affordance disappears on its own.
	if status, data := e.reqAs(t, vendor, "POST", "/api/p/"+cr.ShortCode+"/claim", conv.Tokens["vendor"], map[string]any{}); status != 200 {
		t.Fatalf("vendor claim: status %d, body %s", status, data)
	}
	if got := pendingInvites(t, viewAs(t, e, broker, cr.ShortCode)); len(got) != 0 {
		t.Fatalf("broker pending_invites after vendor claim = %v, want none", got)
	}
}
