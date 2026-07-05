package httpapi_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"escrowpay/internal/gateway/mock"
	"escrowpay/internal/pocket"
)

// instantP2P is a delivery-only pocket: no inspection window, so a valid code
// settles synchronously.
func instantP2P() map[string]any {
	m := vendorInitiatedP2P()
	m["mode"] = "instant"
	m["inspection_window_minutes"] = 0
	return m
}

// fundPocket takes a fresh pocket all the way to FUNDED: the cast's vendor
// creates it, the buyer claims and accepts, and the sandbox funds it.
func fundPocket(t *testing.T, e *testEnv, c cast, body map[string]any) createResp {
	t.Helper()
	cr := createPocket(t, e, c.vendor, body)
	claimAndAccept(t, e, c.buyer, cr)
	if status, data := e.req(t, "POST", "/api/demo/pockets/"+cr.PocketID+"/simulate-funding", "", nil); status != 200 {
		t.Fatalf("simulate-funding: %d %s", status, data)
	}
	return cr
}

// releaseCodeOf fetches the buyer's plaintext Release Code.
func releaseCodeOf(t *testing.T, e *testEnv, c cast, cr createResp) string {
	t.Helper()
	_, data := e.reqAs(t, c.buyer, "GET", "/api/pockets/"+cr.PocketID+"/release-code", "", nil)
	return decode[struct {
		ReleaseCode string `json:"release_code"`
	}](t, data).ReleaseCode
}

// enterCode has the vendor submit a code and returns the decoded result.
func enterCode(t *testing.T, e *testEnv, c cast, cr createResp, code string) (int, enterCodeResp) {
	t.Helper()
	status, data := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/enter-code", "", map[string]any{"code": code})
	if status != 200 {
		return status, enterCodeResp{}
	}
	return status, decode[enterCodeResp](t, data)
}

type enterCodeResp struct {
	Accepted          bool   `json:"accepted"`
	State             string `json:"state"`
	Locked            bool   `json:"locked"`
	AttemptsRemaining int    `json:"attempts_remaining"`
}

// stateOf reads a pocket's current state through the buyer view.
func stateOf(t *testing.T, e *testEnv, c cast, cr createResp) string {
	t.Helper()
	_, data := e.reqAs(t, c.buyer, "GET", "/api/p/"+cr.ShortCode, "", nil)
	s, _ := decode[map[string]any](t, data)["state"].(string)
	return s
}

// wrongCode returns a 4-digit code guaranteed to differ from code.
func wrongCode(code string) string {
	n, _ := strconv.Atoi(code)
	return fmt.Sprintf("%04d", (n+1)%10000)
}

func countCalls(calls []mock.Call, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func settlementRowCount(t *testing.T, pocketID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM settlements WHERE pocket_id = $1`, pocketID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCodeEntryHappyPathSettles walks funded → correct code → DELIVERED_PENDING,
// then advances past the inspection window and lets the sweeper settle (#6, #11).
func TestCodeEntryHappyPathSettles(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)

	_, res := enterCode(t, e, c, cr, code)
	if !res.Accepted || res.State != "DELIVERED_PENDING" {
		t.Fatalf("code entry = %+v, want accepted DELIVERED_PENDING", res)
	}

	// Not yet settleable: a sweep before the window closes is a no-op.
	if rep := e.tick(t); rep.Settled != 0 {
		t.Fatalf("premature settle: %+v", rep)
	}

	e.clock.advance(1441 * time.Minute) // past the 24h inspection window
	if rep := e.tick(t); rep.Settled != 1 {
		t.Fatalf("sweep report = %+v, want 1 settled", rep)
	}
	if got := stateOf(t, e, c, cr); got != "SETTLED" {
		t.Fatalf("state = %s, want SETTLED", got)
	}

	// Exactly one confirmed payout of the vendor allocation (premium retained).
	var direction, role, status string
	var amount int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT direction, beneficiary_role, amount_kobo, status FROM settlements WHERE pocket_id = $1`, cr.PocketID).
		Scan(&direction, &role, &amount, &status); err != nil {
		t.Fatal(err)
	}
	if direction != "payout" || role != "vendor" || amount != 1000000 || status != "confirmed" {
		t.Fatalf("settlement = %s/%s/%d/%s, want payout/vendor/1000000/confirmed", direction, role, amount, status)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 1 {
		t.Fatalf("payout gateway calls = %d, want 1", n)
	}
}

// TestInstantModeSettlesOnCodeEntry proves an instant-mode pocket settles
// synchronously at handoff, without waiting for a sweep.
func TestInstantModeSettlesOnCodeEntry(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, instantP2P())
	code := releaseCodeOf(t, e, c, cr)

	_, res := enterCode(t, e, c, cr, code)
	if !res.Accepted || res.State != "SETTLED" {
		t.Fatalf("instant code entry = %+v, want accepted SETTLED", res)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 1 {
		t.Fatalf("payout calls = %d, want 1 immediate payout", n)
	}
}

// TestCodeLockout proves five wrong entries lock the code, notify both parties,
// and that a correct code after lockout is rejected with 423.
func TestCodeLockout(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)
	bad := wrongCode(code)

	for i := 1; i <= 5; i++ {
		_, res := enterCode(t, e, c, cr, bad)
		if res.Accepted {
			t.Fatalf("attempt %d unexpectedly accepted", i)
		}
		wantRemaining := 5 - i
		if res.AttemptsRemaining != wantRemaining {
			t.Fatalf("attempt %d: remaining = %d, want %d", i, res.AttemptsRemaining, wantRemaining)
		}
		if locked := (i == 5); res.Locked != locked {
			t.Fatalf("attempt %d: locked = %v, want %v", i, res.Locked, locked)
		}
	}

	// Both parties were notified of the lockout.
	if logs := e.logs.String(); !strings.Contains(logs, pocket.NotifyCodeLocked) {
		t.Fatalf("expected a %q notification in logs", pocket.NotifyCodeLocked)
	}

	// A correct code after lockout is refused (423 Locked).
	if status, _ := enterCode(t, e, c, cr, code); status != 423 {
		t.Fatalf("correct code after lockout status = %d, want 423", status)
	}
	if got := stateOf(t, e, c, cr); got != "FUNDED" {
		t.Fatalf("state after lockout = %s, want FUNDED", got)
	}
}

// TestDeliveryDeadlineFreezeThenVendorRefund covers #7 (freeze on lapse) and the
// vendor-confirmed failure branch of #9 (immediate refund).
func TestDeliveryDeadlineFreezeThenVendorRefund(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())

	e.clock.advance(2881 * time.Minute) // past the 48h delivery window
	if rep := e.tick(t); rep.Frozen != 1 {
		t.Fatalf("sweep report = %+v, want 1 frozen", rep)
	}
	if got := stateOf(t, e, c, cr); got != "FROZEN" {
		t.Fatalf("state = %s, want FROZEN", got)
	}

	// The vendor concedes the failed delivery; the buyer is refunded at once.
	if status, data := e.reqAs(t, c.vendor, "POST", "/api/p/"+cr.ShortCode+"/confirm-dispatch-failure", "", nil); status != 200 {
		t.Fatalf("confirm-dispatch-failure: %d %s", status, data)
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
	if role != "buyer" || amount != 1010000 {
		t.Fatalf("refund = %s/%d, want buyer/1010000", role, amount)
	}
}

// TestGraceRefundRequiresAttestation proves the no-auto-refund invariant: a
// frozen pocket refunds on grace lapse only when the buyer has attested
// non-receipt (#9), and never on a timer alone.
func TestGraceRefundRequiresAttestation(t *testing.T) {
	t.Run("no attestation stays frozen", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := fundPocket(t, e, c, vendorInitiatedP2P())
		e.clock.advance(2881 * time.Minute)
		e.tick(t) // -> FROZEN

		e.clock.advance(25 * time.Hour) // past the 24h grace period
		if rep := e.tick(t); rep.GraceRefunded != 0 {
			t.Fatalf("grace refunded without attestation: %+v", rep)
		}
		if got := stateOf(t, e, c, cr); got != "FROZEN" {
			t.Fatalf("state = %s, want FROZEN (no auto-refund)", got)
		}
		if n := settlementRowCount(t, cr.PocketID); n != 0 {
			t.Fatalf("settlement rows = %d, want 0", n)
		}
	})

	t.Run("attestation then grace lapse refunds", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := fundPocket(t, e, c, vendorInitiatedP2P())
		e.clock.advance(2881 * time.Minute)
		e.tick(t) // -> FROZEN

		if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/attest-non-receipt", "", nil); status != 200 {
			t.Fatalf("attest: %d %s", status, data)
		}
		// Still frozen until the grace period actually lapses.
		if got := stateOf(t, e, c, cr); got != "FROZEN" {
			t.Fatalf("state after attest = %s, want FROZEN", got)
		}

		e.clock.advance(25 * time.Hour)
		if rep := e.tick(t); rep.GraceRefunded != 1 {
			t.Fatalf("sweep report = %+v, want 1 grace refund", rep)
		}
		if got := stateOf(t, e, c, cr); got != "REFUNDED" {
			t.Fatalf("state = %s, want REFUNDED", got)
		}
		if n := countCalls(e.gateway.Calls(), "Refund"); n != 1 {
			t.Fatalf("refund calls = %d, want 1", n)
		}
	})
}

// TestFrozenLateCodeEntry proves a late Release Code still delivers a frozen
// pocket (#8): FROZEN → DELIVERED_PENDING.
func TestFrozenLateCodeEntry(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)

	e.clock.advance(2881 * time.Minute)
	e.tick(t) // -> FROZEN
	if got := stateOf(t, e, c, cr); got != "FROZEN" {
		t.Fatalf("precondition state = %s, want FROZEN", got)
	}

	_, res := enterCode(t, e, c, cr, code)
	if !res.Accepted || res.State != "DELIVERED_PENDING" {
		t.Fatalf("late code entry = %+v, want accepted DELIVERED_PENDING", res)
	}
}

// TestDisputeVersusSettleRace fires a buyer dispute and a settlement sweep at the
// exact inspection boundary. The domain's guards are mutually exclusive, so
// settlement wins the boundary and the dispute is rejected — exactly one outcome,
// and a pocket disputed before the boundary never later auto-settles.
func TestDisputeVersusSettleRace(t *testing.T) {
	t.Run("boundary: settlement wins, dispute rejected", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := fundPocket(t, e, c, vendorInitiatedP2P())
		code := releaseCodeOf(t, e, c, cr)
		enterCode(t, e, c, cr, code) // -> DELIVERED_PENDING, settle_after = now + 24h

		e.clock.advance(1440 * time.Minute) // exactly at settle_after

		var (
			wg           sync.WaitGroup
			disputeState int
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			disputeState, _ = e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil)
		}()
		go func() {
			defer wg.Done()
			e.tick(t)
		}()
		wg.Wait()

		// The dispute is refused at/after the boundary regardless of lock order.
		if disputeState != 409 {
			t.Fatalf("dispute at boundary status = %d, want 409", disputeState)
		}
		// Ensure settlement completes (a follow-up tick covers the skip-locked case).
		e.tick(t)
		if got := stateOf(t, e, c, cr); got != "SETTLED" {
			t.Fatalf("state = %s, want SETTLED", got)
		}

		// The audit shows a settlement and never a dispute.
		var disputes int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM pocket_events WHERE pocket_id = $1 AND to_state = 'DISPUTED'`, cr.PocketID).
			Scan(&disputes); err != nil {
			t.Fatal(err)
		}
		if disputes != 0 {
			t.Fatalf("dispute events = %d, want 0", disputes)
		}
	})

	t.Run("dispute before boundary blocks settlement", func(t *testing.T) {
		e := newTestEnv(t)
		c := stdCast(t, e)
		cr := fundPocket(t, e, c, vendorInitiatedP2P())
		code := releaseCodeOf(t, e, c, cr)
		enterCode(t, e, c, cr, code)

		e.clock.advance(1439 * time.Minute) // still inside the window
		if status, data := e.reqAs(t, c.buyer, "POST", "/api/p/"+cr.ShortCode+"/report-issue", "", nil); status != 200 {
			t.Fatalf("report-issue: %d %s", status, data)
		}
		if got := stateOf(t, e, c, cr); got != "DISPUTED" {
			t.Fatalf("state = %s, want DISPUTED", got)
		}

		// The window elapses, but a disputed pocket is never swept into settlement.
		e.clock.advance(10 * time.Minute)
		if rep := e.tick(t); rep.Settled != 0 {
			t.Fatalf("disputed pocket was settled: %+v", rep)
		}
		if got := stateOf(t, e, c, cr); got != "DISPUTED" {
			t.Fatalf("state = %s, want DISPUTED", got)
		}
		if n := settlementRowCount(t, cr.PocketID); n != 0 {
			t.Fatalf("settlement rows for disputed pocket = %d, want 0", n)
		}
	})
}

// TestDuplicateSweepNoDoublePay proves repeated sweeper passes never move money
// twice: once a pocket has settled, further ticks make no new gateway calls.
func TestDuplicateSweepNoDoublePay(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)
	enterCode(t, e, c, cr, code)

	e.clock.advance(1441 * time.Minute)
	e.tick(t) // settles
	first := countCalls(e.gateway.Calls(), "Payout")
	if first != 1 {
		t.Fatalf("payout calls after first settle = %d, want 1", first)
	}

	for i := 0; i < 3; i++ {
		e.tick(t)
	}
	if again := countCalls(e.gateway.Calls(), "Payout"); again != 1 {
		t.Fatalf("payout calls after repeated sweeps = %d, want 1 (no double pay)", again)
	}
}

// TestCrashRecoveryOfPendingLegs simulates a crash between committing a
// settlement and disbursing it: the transition records its legs in-transaction
// but the process dies before payment. A later sweeper pass reconciles the
// pending leg and pays it exactly once.
func TestCrashRecoveryOfPendingLegs(t *testing.T) {
	e := newTestEnv(t)
	c := stdCast(t, e)
	cr := fundPocket(t, e, c, vendorInitiatedP2P())
	code := releaseCodeOf(t, e, c, cr)
	enterCode(t, e, c, cr, code) // -> DELIVERED_PENDING

	// Drive settlement through the store's write path directly, bypassing the
	// application's post-commit disbursement — the leg is committed but unpaid,
	// exactly the state a crash would leave behind.
	e.clock.advance(1441 * time.Minute)
	if _, err := e.store.RunTransition(context.Background(), cr.PocketID, "system",
		pocket.Event{Kind: pocket.EvSettleDue}, e.clock.now()); err != nil {
		t.Fatalf("direct settle: %v", err)
	}
	if got := stateOf(t, e, c, cr); got != "SETTLED" {
		t.Fatalf("state = %s, want SETTLED", got)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 0 {
		t.Fatalf("payout before recovery = %d, want 0 (leg still pending)", n)
	}

	// Reconciliation pays the orphaned leg.
	if rep := e.tick(t); rep.LegsReconciled != 1 {
		t.Fatalf("sweep report = %+v, want 1 leg reconciled", rep)
	}
	if n := countCalls(e.gateway.Calls(), "Payout"); n != 1 {
		t.Fatalf("payout after recovery = %d, want 1", n)
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM settlements WHERE pocket_id = $1 AND direction = 'payout'`, cr.PocketID).
		Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "confirmed" {
		t.Fatalf("leg status = %s, want confirmed", status)
	}
}
