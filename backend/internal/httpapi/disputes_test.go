package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

type evidenceResp struct {
	ID           string `json:"id"`
	Party        string `json:"party"`
	Type         string `json:"type"`
	WithinWindow *bool  `json:"within_window"`
}

type disputeResp struct {
	Class      string `json:"class"`
	State      string `json:"state"`
	Resolution string `json:"resolution"`
	Evidence   []struct {
		Type         string `json:"type"`
		WithinWindow *bool  `json:"within_window"`
	} `json:"evidence"`
}

// deliveredPocket takes a cooldown pocket to DELIVERED_PENDING via a valid code.
func deliveredPocket(t *testing.T, e *testEnv, c cast) createResp {
	t.Helper()
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)
	if _, res := enterCode(t, e, c, cr, code); res.State != "DELIVERED_PENDING" {
		t.Fatalf("precondition state = %s, want DELIVERED_PENDING", res.State)
	}
	return cr
}

// uploadEvidence posts a multipart evidence file as the given participant.
func uploadEvidence(t *testing.T, e *testEnv, ac *actor, cr createResp, evType, filename string, content []byte) (int, evidenceResp) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("type", evType); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", e.server.URL+"/api/p/"+cr.ShortCode+"/evidence", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(ac.cookie)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		return resp.StatusCode, evidenceResp{}
	}
	var ev evidenceResp
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("decode evidence %s: %v", data, err)
	}
	return resp.StatusCode, ev
}

func disputeOf(t *testing.T, e *testEnv, ac *actor, cr createResp) disputeResp {
	t.Helper()
	_, data := e.reqAs(t, ac, "GET", "/api/p/"+cr.ShortCode+"/dispute", "", nil)
	return decode[disputeResp](t, data)
}

func userStrikes(t *testing.T, pocketID, role string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT u.strikes FROM users u
		JOIN pocket_participants pp ON pp.user_id = u.id
		WHERE pp.pocket_id = $1 AND pp.role = $2`, pocketID, role).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestReportEvidenceConcession walks the buyer's not-as-described path: report an
// issue (#12), attach in-app unboxing evidence, then the vendor concedes (#13),
// refunding exactly once.
func TestReportEvidenceConcession(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := deliveredPocket(t, e, c)

	if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil); status != 200 {
		t.Fatalf("report-issue: %d %s", status, data)
	}

	d := disputeOf(t, e, c.buyer, cr)
	if d.Class != "not_as_described" || d.State != "open" {
		t.Fatalf("dispute = %+v, want not_as_described/open", d)
	}

	// Buyer records an unboxing video at handoff time — inside the window.
	status, ev := uploadEvidence(t, e, c.buyer, cr, "unboxing_video", "unbox.mp4", []byte("video-bytes"))
	if status != 201 {
		t.Fatalf("evidence upload status = %d", status)
	}
	if ev.WithinWindow == nil || !*ev.WithinWindow {
		t.Fatalf("within_window = %v, want true", ev.WithinWindow)
	}

	// The vendor concedes; the buyer is refunded.
	if status, data := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/concede", "", nil); status != 200 {
		t.Fatalf("concede: %d %s", status, data)
	}
	if got := stateOf(t, e, c, cr); got != "REFUNDED" {
		t.Fatalf("state = %s, want REFUNDED", got)
	}

	// Exactly one refund, and a second concede is refused (terminal).
	if n := settlementRowCount(t, cr.PocketID); n != 1 {
		t.Fatalf("settlement rows = %d, want 1", n)
	}
	if n := countCalls(e.gateway.Calls(), "Refund"); n != 1 {
		t.Fatalf("refund calls = %d, want 1", n)
	}
	if status, _ := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/concede", "", nil); status != 409 {
		t.Fatalf("second concede status = %d, want 409", status)
	}

	// The dispute is now recorded as resolved by refund.
	d = disputeOf(t, e, c.buyer, cr)
	if d.State != "resolved" || d.Resolution != "refund" {
		t.Fatalf("resolved dispute = %+v, want resolved/refund", d)
	}
	if len(d.Evidence) != 1 || d.Evidence[0].Type != "unboxing_video" {
		t.Fatalf("evidence in dispute view = %+v, want one unboxing_video", d.Evidence)
	}
}

// TestEvidenceWindowEnforcement proves the 60-minute in-app unboxing window is
// enforced server-side from the handoff timestamp.
func TestEvidenceWindowEnforcement(t *testing.T) {
	t.Run("within window", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := deliveredPocket(t, e, c)
		e.clock.advance(30 * time.Minute)
		_, ev := uploadEvidence(t, e, c.buyer, cr, "unboxing_video", "unbox.mp4", []byte("v"))
		if ev.WithinWindow == nil || !*ev.WithinWindow {
			t.Fatalf("within_window = %v, want true at +30m", ev.WithinWindow)
		}
	})

	t.Run("past window", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := deliveredPocket(t, e, c)
		e.clock.advance(61 * time.Minute)
		_, ev := uploadEvidence(t, e, c.buyer, cr, "unboxing_video", "unbox.mp4", []byte("v"))
		if ev.WithinWindow == nil || *ev.WithinWindow {
			t.Fatalf("within_window = %v, want false at +61m", ev.WithinWindow)
		}
	})
}

// TestFrozenDisputeThenAdminForceRefund covers the not-delivered path: a frozen
// pocket is escalated (#10), appears in the admin queue, and admin force-refund
// (#14) refunds the buyer and strikes the vendor.
func TestFrozenDisputeThenAdminForceRefund(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	e.clock.advance(2881 * time.Minute)
	e.tick(t) // -> FROZEN

	if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/dispute", "", nil); status != 200 {
		t.Fatalf("dispute: %d %s", status, data)
	}
	if got := stateOf(t, e, c, cr); got != "DISPUTED" {
		t.Fatalf("state = %s, want DISPUTED", got)
	}
	if d := disputeOf(t, e, c.buyer, cr); d.Class != "not_delivered" {
		t.Fatalf("class = %s, want not_delivered", d.Class)
	}

	// The dispute is on the admin arbitration queue.
	admin := e.loginAdmin(t)
	_, qdata := e.reqAs(t, admin, "GET", "/api/admin/disputes", "", nil)
	queue := decode[struct {
		Disputes []struct {
			PocketID string `json:"pocket_id"`
			Class    string `json:"class"`
		} `json:"disputes"`
	}](t, qdata)
	if len(queue.Disputes) != 1 || queue.Disputes[0].PocketID != cr.PocketID {
		t.Fatalf("dispute queue = %+v, want the one pocket", queue.Disputes)
	}

	// Admin rules for the buyer and flags the vendor.
	if status, data := e.reqAs(t, admin, "POST", "/api/admin/pockets/"+cr.PocketID+"/force-refund", "", nil); status != 200 {
		t.Fatalf("force-refund: %d %s", status, data)
	}
	if got := stateOf(t, e, c, cr); got != "REFUNDED" {
		t.Fatalf("state = %s, want REFUNDED", got)
	}
	if n := countCalls(e.gateway.Calls(), "Refund"); n != 1 {
		t.Fatalf("refund calls = %d, want 1", n)
	}
	if s := userStrikes(t, cr.PocketID, "vendor"); s != 1 {
		t.Fatalf("vendor strikes = %d, want 1 (fraud flag)", s)
	}
	// The queue is empty once resolved.
	_, qdata = e.reqAs(t, admin, "GET", "/api/admin/disputes", "", nil)
	if q := decode[struct {
		Disputes []json.RawMessage `json:"disputes"`
	}](t, qdata); len(q.Disputes) != 0 {
		t.Fatalf("queue after resolution = %d, want 0", len(q.Disputes))
	}
}

// TestAdminForcePayoutBadFaith covers #15: admin releases funds to the vendor and
// strikes the bad-faith buyer.
func TestAdminForcePayoutBadFaith(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := deliveredPocket(t, e, c)
	if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil); status != 200 {
		t.Fatalf("report-issue: %d %s", status, data)
	}

	admin := e.loginAdmin(t)
	if status, data := e.reqAs(t, admin, "POST", "/api/admin/pockets/"+cr.PocketID+"/force-payout", "", map[string]any{"bad_faith": true}); status != 200 {
		t.Fatalf("force-payout: %d %s", status, data)
	}
	if got := stateOf(t, e, c, cr); got != "SETTLED" {
		t.Fatalf("state = %s, want SETTLED", got)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 1 {
		t.Fatalf("payout calls = %d, want 1", n)
	}
	if s := userStrikes(t, cr.PocketID, "buyer"); s != 1 {
		t.Fatalf("buyer strikes = %d, want 1 (bad faith)", s)
	}
}

// TestDisputeAuthorization checks the resolution guards: a buyer cannot concede,
// and the admin arbitration surface is closed when sandbox mode is off.
func TestDisputeAuthorization(t *testing.T) {
	t.Run("buyer cannot concede", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := deliveredPocket(t, e, c)
		e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil)
		if status, _ := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/concede", "", nil); status != 403 {
			t.Fatalf("buyer concede status = %d, want 403", status)
		}
	})

	t.Run("admin surface requires an admin account", func(t *testing.T) {
		e := newEnv(t, envOptions{sandbox: false})
		id := "00000000-0000-0000-0000-000000000000"

		// Anonymous callers are asked to sign in.
		if status, _ := e.req(t, "GET", "/api/admin/disputes", "", nil); status != 401 {
			t.Fatalf("anonymous dispute queue status = %d, want 401", status)
		}
		// Outside sandbox the demo login is closed, so no admin can be minted.
		if status, _ := e.req(t, "POST", "/api/auth/demo", "", map[string]any{"phone": "+2348000000000", "admin": true}); status != 403 {
			t.Fatalf("demo login outside sandbox status = %d, want 403", status)
		}
		// A signed-in non-admin is refused.
		u, err := e.store.UpsertUserByPhone(context.Background(), "+2348011111111", "Plain User")
		if err != nil {
			t.Fatal(err)
		}
		plain := e.sessionFor(t, u.ID)
		if status, _ := e.reqAs(t, plain, "GET", "/api/admin/disputes", "", nil); status != 403 {
			t.Fatalf("non-admin dispute queue status = %d, want 403", status)
		}
		if status, _ := e.reqAs(t, plain, "POST", "/api/admin/pockets/"+id+"/force-refund", "", nil); status != 403 {
			t.Fatalf("non-admin force-refund status = %d, want 403", status)
		}
		if status, _ := e.reqAs(t, plain, "POST", "/api/admin/pockets/"+id+"/force-payout", "", nil); status != 403 {
			t.Fatalf("non-admin force-payout status = %d, want 403", status)
		}
	})
}
