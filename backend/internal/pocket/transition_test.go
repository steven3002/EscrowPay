package pocket

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

// containsEffect reports whether effects holds at least one value of type T.
func containsEffect[T Effect](effects []Effect) bool {
	for _, e := range effects {
		if _, ok := e.(T); ok {
			return true
		}
	}
	return false
}

// mkPocket builds a cooldown p2p pocket in the given state, applying mut for
// per-case fields such as timers.
func mkPocket(state State, mut func(*Pocket)) Pocket {
	p := Pocket{
		State:                 state,
		Structure:             StructureP2P,
		CreatorRole:           RoleVendor,
		Mode:                  ModeCooldown,
		InspectionWindow:      24 * time.Hour,
		DeliveryWindow:        48 * time.Hour,
		AmountKobo:            1_000_000,
		PremiumKobo:           20_000,
		GracePeriod:           DefaultGracePeriod,
		EvidenceCaptureWindow: DefaultEvidenceCaptureWindow,
	}
	if mut != nil {
		mut(&p)
	}
	return p
}

// TestLegalTransitions asserts edges #2–#15. Edge #1 (New) is covered in
// pocket_test.go, completing 15/15.
func TestLegalTransitions(t *testing.T) {
	cases := []struct {
		name   string
		from   Pocket
		now    time.Time
		ev     Event
		want   State
		assert func(t *testing.T, out Outcome)
	}{
		{
			name: "#2 CREATED->FUNDED",
			from: mkPocket(StateCreated, nil),
			now:  testNow,
			ev:   Event{Kind: EvFundingConfirmed},
			want: StateFunded,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[GenerateReleaseCode](out.Effects) {
					t.Fatal("expected GenerateReleaseCode effect")
				}
				if !out.Pocket.DeliveryDeadline.Equal(testNow.Add(48 * time.Hour)) {
					t.Fatalf("delivery deadline = %v", out.Pocket.DeliveryDeadline)
				}
			},
		},
		{
			name: "#3 CREATED->EXPIRED",
			from: mkPocket(StateCreated, func(p *Pocket) { p.FundingExpiresAt = testNow.Add(-time.Minute) }),
			now:  testNow,
			ev:   Event{Kind: EvFundingExpired},
			want: StateExpired,
		},
		{
			name: "#4 CREATED->CANCELLED",
			from: mkPocket(StateCreated, nil),
			now:  testNow,
			ev:   Event{Kind: EvCancel},
			want: StateCancelled,
		},
		{
			name: "#5 FUNDED->REFUNDED",
			from: mkPocket(StateFunded, nil),
			now:  testNow,
			ev:   Event{Kind: EvVendorCancel},
			want: StateRefunded,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[ExecuteRefund](out.Effects) {
					t.Fatal("expected ExecuteRefund effect")
				}
			},
		},
		{
			name: "#6 FUNDED->DELIVERED_PENDING",
			from: mkPocket(StateFunded, nil),
			now:  testNow,
			ev:   Event{Kind: EvCodeAccepted},
			want: StateDeliveredPending,
			assert: func(t *testing.T, out Outcome) {
				if !out.Pocket.SettleAfter.Equal(testNow.Add(24 * time.Hour)) {
					t.Fatalf("settle_after = %v", out.Pocket.SettleAfter)
				}
				if !containsEffect[PromptEvidence](out.Effects) {
					t.Fatal("expected PromptEvidence effect")
				}
			},
		},
		{
			name: "#7 FUNDED->FROZEN",
			from: mkPocket(StateFunded, func(p *Pocket) { p.DeliveryDeadline = testNow.Add(-time.Minute) }),
			now:  testNow,
			ev:   Event{Kind: EvDeliveryDeadlineLapsed},
			want: StateFrozen,
			assert: func(t *testing.T, out Outcome) {
				if out.Pocket.GraceDeadline.IsZero() {
					t.Fatal("grace deadline not set")
				}
				if !containsEffect[StartGrace](out.Effects) {
					t.Fatal("expected StartGrace effect")
				}
			},
		},
		{
			name: "#8 FROZEN->DELIVERED_PENDING",
			from: mkPocket(StateFrozen, func(p *Pocket) { p.GraceDeadline = testNow.Add(20 * time.Hour) }),
			now:  testNow,
			ev:   Event{Kind: EvCodeAccepted},
			want: StateDeliveredPending,
		},
		{
			name: "#9 FROZEN->REFUNDED",
			from: mkPocket(StateFrozen, nil),
			now:  testNow,
			ev:   Event{Kind: EvRefundFrozen, Reason: "vendor_failure"},
			want: StateRefunded,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[ExecuteRefund](out.Effects) {
					t.Fatal("expected ExecuteRefund effect")
				}
			},
		},
		{
			name: "#10 FROZEN->DISPUTED",
			from: mkPocket(StateFrozen, nil),
			now:  testNow,
			ev:   Event{Kind: EvFrozenDispute},
			want: StateDisputed,
			assert: func(t *testing.T, out Outcome) {
				if out.Pocket.DisputeClass != DisputeNotDelivered {
					t.Fatalf("class = %q", out.Pocket.DisputeClass)
				}
			},
		},
		{
			name: "#11 DELIVERED_PENDING->SETTLED",
			from: mkPocket(StateDeliveredPending, func(p *Pocket) { p.SettleAfter = testNow.Add(-time.Minute) }),
			now:  testNow,
			ev:   Event{Kind: EvSettleDue},
			want: StateSettled,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[SchedulePayout](out.Effects) {
					t.Fatal("expected SchedulePayout effect")
				}
			},
		},
		{
			name: "#12 DELIVERED_PENDING->DISPUTED",
			from: mkPocket(StateDeliveredPending, func(p *Pocket) { p.SettleAfter = testNow.Add(time.Hour) }),
			now:  testNow,
			ev:   Event{Kind: EvBuyerReportIssue},
			want: StateDisputed,
			assert: func(t *testing.T, out Outcome) {
				if out.Pocket.DisputeClass != DisputeNotAsDescribed {
					t.Fatalf("class = %q", out.Pocket.DisputeClass)
				}
			},
		},
		{
			name: "#13 DISPUTED->REFUNDED (concede)",
			from: mkPocket(StateDisputed, func(p *Pocket) { p.DisputeClass = DisputeNotAsDescribed }),
			now:  testNow,
			ev:   Event{Kind: EvVendorConcede},
			want: StateRefunded,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[ExecuteRefund](out.Effects) {
					t.Fatal("expected ExecuteRefund effect")
				}
			},
		},
		{
			name: "#14 DISPUTED->REFUNDED (force)",
			from: mkPocket(StateDisputed, func(p *Pocket) { p.DisputeClass = DisputeNotAsDescribed }),
			now:  testNow,
			ev:   Event{Kind: EvAdminForceRefund},
			want: StateRefunded,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[Sanction](out.Effects) {
					t.Fatal("expected Sanction effect")
				}
			},
		},
		{
			name: "#15 DISPUTED->SETTLED (force, bad faith)",
			from: mkPocket(StateDisputed, func(p *Pocket) { p.DisputeClass = DisputeNotDelivered }),
			now:  testNow,
			ev:   Event{Kind: EvAdminForcePayout, BadFaith: true},
			want: StateSettled,
			assert: func(t *testing.T, out Outcome) {
				if !containsEffect[SchedulePayout](out.Effects) {
					t.Fatal("expected SchedulePayout effect")
				}
				if !containsEffect[Sanction](out.Effects) {
					t.Fatal("expected buyer Sanction on bad-faith payout")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.from.Transition(tc.now, tc.ev)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Pocket.State != tc.want {
				t.Fatalf("state = %s, want %s", out.Pocket.State, tc.want)
			}
			if tc.assert != nil {
				tc.assert(t, out)
			}
		})
	}
}

// TestIllegalTransitions asserts rejected edges, including every terminal state.
func TestIllegalTransitions(t *testing.T) {
	cases := []struct {
		name    string
		from    Pocket
		ev      Event
		wantErr error
	}{
		{"CREATED+code_accepted", mkPocket(StateCreated, nil), Event{Kind: EvCodeAccepted}, ErrIllegalTransition},
		{"CREATED+settle_due", mkPocket(StateCreated, nil), Event{Kind: EvSettleDue}, ErrIllegalTransition},
		{"CREATED+report_issue", mkPocket(StateCreated, nil), Event{Kind: EvBuyerReportIssue}, ErrIllegalTransition},
		{"FUNDED+funding_confirmed", mkPocket(StateFunded, nil), Event{Kind: EvFundingConfirmed}, ErrIllegalTransition},
		{"FUNDED+settle_due", mkPocket(StateFunded, nil), Event{Kind: EvSettleDue}, ErrIllegalTransition},
		{"FUNDED+vendor_concede", mkPocket(StateFunded, nil), Event{Kind: EvVendorConcede}, ErrIllegalTransition},
		{"DELIVERED_PENDING+funding_confirmed", mkPocket(StateDeliveredPending, func(p *Pocket) { p.SettleAfter = testNow.Add(time.Hour) }), Event{Kind: EvFundingConfirmed}, ErrIllegalTransition},
		{"DELIVERED_PENDING+code_accepted", mkPocket(StateDeliveredPending, func(p *Pocket) { p.SettleAfter = testNow.Add(time.Hour) }), Event{Kind: EvCodeAccepted}, ErrIllegalTransition},
		{"FROZEN+settle_due", mkPocket(StateFrozen, nil), Event{Kind: EvSettleDue}, ErrIllegalTransition},
		{"DISPUTED+code_accepted", mkPocket(StateDisputed, func(p *Pocket) { p.DisputeClass = DisputeNotDelivered }), Event{Kind: EvCodeAccepted}, ErrIllegalTransition},
		{"SETTLED+settle_due (terminal)", mkPocket(StateSettled, nil), Event{Kind: EvSettleDue}, ErrTerminal},
		{"REFUNDED+cancel (terminal)", mkPocket(StateRefunded, nil), Event{Kind: EvCancel}, ErrTerminal},
		{"CANCELLED+funding_confirmed (terminal)", mkPocket(StateCancelled, nil), Event{Kind: EvFundingConfirmed}, ErrTerminal},
		{"EXPIRED+funding_confirmed (terminal)", mkPocket(StateExpired, nil), Event{Kind: EvFundingConfirmed}, ErrTerminal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.from.Transition(testNow, tc.ev)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestSettleDisputeBoundary pins the mutually exclusive guards where #11 and #12
// meet: dispute is allowed strictly before settle_after, settlement at or after.
func TestSettleDisputeBoundary(t *testing.T) {
	settleAt := testNow
	p := mkPocket(StateDeliveredPending, func(p *Pocket) { p.SettleAfter = settleAt })

	before := settleAt.Add(-time.Nanosecond)
	if _, err := p.Transition(before, Event{Kind: EvBuyerReportIssue}); err != nil {
		t.Fatalf("dispute strictly before boundary should succeed: %v", err)
	}
	if _, err := p.Transition(before, Event{Kind: EvSettleDue}); !errors.Is(err, ErrNotYetDue) {
		t.Fatalf("settle before boundary: err = %v, want ErrNotYetDue", err)
	}

	if _, err := p.Transition(settleAt, Event{Kind: EvSettleDue}); err != nil {
		t.Fatalf("settle at boundary should succeed: %v", err)
	}
	if _, err := p.Transition(settleAt, Event{Kind: EvBuyerReportIssue}); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("dispute at boundary: err = %v, want ErrWindowClosed", err)
	}
}
