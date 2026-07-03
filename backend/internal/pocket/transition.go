package pocket

import (
	"fmt"
	"time"

	"escrowpay/internal/releasecode"
)

// EventKind enumerates the events that drive the state machine. Each maps to at
// most one edge in the transition table (see the package documentation);
// EvCodeRejected mutates attempt state without changing the pocket's state.
type EventKind string

const (
	EvFundingConfirmed       EventKind = "funding_confirmed"        // #2
	EvFundingExpired         EventKind = "funding_expired"          // #3
	EvCancel                 EventKind = "cancel"                   // #4
	EvVendorCancel           EventKind = "vendor_cancel"            // #5
	EvCodeAccepted           EventKind = "code_accepted"            // #6 / #8
	EvCodeRejected           EventKind = "code_rejected"            // attempt policy
	EvDeliveryDeadlineLapsed EventKind = "delivery_deadline_lapsed" // #7
	EvRefundFrozen           EventKind = "refund_frozen"            // #9
	EvFrozenDispute          EventKind = "frozen_dispute"           // #10
	EvSettleDue              EventKind = "settle_due"               // #11
	EvBuyerReportIssue       EventKind = "buyer_report_issue"       // #12
	EvVendorConcede          EventKind = "vendor_concede"           // #13
	EvAdminForceRefund       EventKind = "admin_force_refund"       // #14
	EvAdminForcePayout       EventKind = "admin_force_payout"       // #15
)

// Notification kinds emitted by transitions. The notification layer maps these
// to concrete message copy per channel.
const (
	NotifyFundsSecured         = "funds_secured"
	NotifyDeliveryConfirmed    = "delivery_confirmed"
	NotifyHandoffConfirmed     = "handoff_confirmed"
	NotifyCodeLocked           = "code_locked"
	NotifyFundingExpired       = "funding_expired"
	NotifyCancelled            = "cancelled"
	NotifyRefundIssued         = "refund_issued"
	NotifyDeliveryWindowClosed = "delivery_window_closed"
	NotifyDisputeOpened        = "dispute_opened"
	NotifySettled              = "settled"
)

// Event is an input to the state machine. Kind is required; the remaining
// fields carry payload for the events that need it.
type Event struct {
	Kind EventKind

	// Reason annotates EvRefundFrozen (e.g. "vendor_failure", "grace_nonreceipt").
	Reason string
	// BadFaith, on EvAdminForcePayout, records a strike against the buyer.
	BadFaith bool
}

// Transition applies ev to the pocket at time now, returning the resulting
// snapshot and the effects to execute. It never mutates the receiver. Terminal
// immutability is enforced once here, before dispatch.
func (p Pocket) Transition(now time.Time, ev Event) (Outcome, error) {
	if p.State.IsTerminal() {
		return Outcome{}, ErrTerminal
	}
	switch ev.Kind {
	case EvFundingConfirmed:
		return p.fundingConfirmed(now)
	case EvFundingExpired:
		return p.fundingExpired(now)
	case EvCancel:
		return p.cancel()
	case EvVendorCancel:
		return p.vendorCancel()
	case EvCodeAccepted:
		return p.codeAccepted(now)
	case EvCodeRejected:
		return p.codeRejected()
	case EvDeliveryDeadlineLapsed:
		return p.deliveryDeadlineLapsed(now)
	case EvRefundFrozen:
		return p.refundFrozen()
	case EvFrozenDispute:
		return p.frozenDispute()
	case EvSettleDue:
		return p.settleDue(now)
	case EvBuyerReportIssue:
		return p.buyerReportIssue(now)
	case EvVendorConcede:
		return p.vendorConcede()
	case EvAdminForceRefund:
		return p.adminForceRefund()
	case EvAdminForcePayout:
		return p.adminForcePayout(ev)
	default:
		return Outcome{}, fmt.Errorf("%w: unknown event %q", ErrIllegalTransition, ev.Kind)
	}
}

// #2 CREATED → FUNDED.
func (p Pocket) fundingConfirmed(now time.Time) (Outcome, error) {
	if p.State != StateCreated {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateFunded
	next.DeliveryDeadline = now.Add(p.DeliveryWindow)
	return Outcome{
		Pocket: next,
		Effects: []Effect{
			GenerateReleaseCode{},
			Notify{Role: RoleVendor, Kind: NotifyFundsSecured},
		},
	}, nil
}

// #3 CREATED → EXPIRED.
func (p Pocket) fundingExpired(now time.Time) (Outcome, error) {
	if p.State != StateCreated {
		return Outcome{}, ErrIllegalTransition
	}
	if !p.FundingExpiresAt.IsZero() && now.Before(p.FundingExpiresAt) {
		return Outcome{}, ErrNotYetDue
	}
	next := p
	next.State = StateExpired
	return Outcome{Pocket: next, Effects: bothNotify(NotifyFundingExpired)}, nil
}

// #4 CREATED → CANCELLED.
func (p Pocket) cancel() (Outcome, error) {
	if p.State != StateCreated {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateCancelled
	return Outcome{Pocket: next, Effects: bothNotify(NotifyCancelled)}, nil
}

// #5 FUNDED → REFUNDED (vendor or mutual cancel after funding).
func (p Pocket) vendorCancel() (Outcome, error) {
	if p.State != StateFunded {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateRefunded
	return Outcome{Pocket: next, Effects: p.refundEffects()}, nil
}

// #6 / #8 FUNDED|FROZEN → DELIVERED_PENDING on a valid Release Code.
func (p Pocket) codeAccepted(now time.Time) (Outcome, error) {
	if p.State != StateFunded && p.State != StateFrozen {
		return Outcome{}, ErrIllegalTransition
	}
	if p.CodeLocked {
		return Outcome{}, ErrCodeLocked
	}
	next := p
	next.State = StateDeliveredPending
	next.SettleAfter = now.Add(p.InspectionWindow)
	next.GraceDeadline = time.Time{}

	effects := make([]Effect, 0, 3)
	// Instant mode is delivery-only: no inspection window, so no evidence prompt.
	if p.Mode == ModeCooldown {
		effects = append(effects, PromptEvidence{CaptureWindow: p.EvidenceCaptureWindow})
	}
	effects = append(effects,
		Notify{Role: RoleBuyer, Kind: NotifyDeliveryConfirmed},
		Notify{Role: RoleVendor, Kind: NotifyHandoffConfirmed},
	)
	return Outcome{Pocket: next, Effects: effects}, nil
}

// codeRejected applies the attempt policy without changing state. The fifth
// failure locks the code and notifies both parties.
func (p Pocket) codeRejected() (Outcome, error) {
	if p.State != StateFunded && p.State != StateFrozen {
		return Outcome{}, ErrIllegalTransition
	}
	if p.CodeLocked {
		return Outcome{}, ErrCodeLocked
	}
	attempts, locked := releasecode.RegisterFailure(p.CodeAttempts)
	next := p
	next.CodeAttempts = attempts
	next.CodeLocked = locked

	var effects []Effect
	if locked {
		effects = bothNotify(NotifyCodeLocked)
	}
	return Outcome{Pocket: next, Effects: effects}, nil
}

// #7 FUNDED → FROZEN when the delivery deadline lapses without a code.
func (p Pocket) deliveryDeadlineLapsed(now time.Time) (Outcome, error) {
	if p.State != StateFunded {
		return Outcome{}, ErrIllegalTransition
	}
	if !p.DeliveryDeadline.IsZero() && now.Before(p.DeliveryDeadline) {
		return Outcome{}, ErrNotYetDue
	}
	next := p
	next.State = StateFrozen
	next.GraceDeadline = now.Add(p.GracePeriod)
	effects := append([]Effect{StartGrace{Until: next.GraceDeadline}}, bothNotify(NotifyDeliveryWindowClosed)...)
	return Outcome{Pocket: next, Effects: effects}, nil
}

// #9 FROZEN → REFUNDED (vendor confirms failure, or grace lapses with a
// buyer non-receipt attestation). The distinguishing trigger is carried in the
// event's Reason for the audit log; both resolve to a refund.
func (p Pocket) refundFrozen() (Outcome, error) {
	if p.State != StateFrozen {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateRefunded
	return Outcome{Pocket: next, Effects: p.refundEffects()}, nil
}

// #10 FROZEN → DISPUTED when the parties disagree on delivery.
func (p Pocket) frozenDispute() (Outcome, error) {
	if p.State != StateFrozen {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateDisputed
	next.DisputeClass = DisputeNotDelivered
	return Outcome{Pocket: next, Effects: p.disputeNotify()}, nil
}

// #11 DELIVERED_PENDING → SETTLED once the inspection window has elapsed.
func (p Pocket) settleDue(now time.Time) (Outcome, error) {
	if p.State != StateDeliveredPending {
		return Outcome{}, ErrIllegalTransition
	}
	if now.Before(p.SettleAfter) {
		return Outcome{}, ErrNotYetDue
	}
	next := p
	next.State = StateSettled
	return Outcome{Pocket: next, Effects: p.settleEffects()}, nil
}

// #12 DELIVERED_PENDING → DISPUTED when the buyer reports an issue strictly
// before settlement. At the boundary (now == settle_after) settlement wins.
func (p Pocket) buyerReportIssue(now time.Time) (Outcome, error) {
	if p.State != StateDeliveredPending {
		return Outcome{}, ErrIllegalTransition
	}
	if !now.Before(p.SettleAfter) {
		return Outcome{}, ErrWindowClosed
	}
	next := p
	next.State = StateDisputed
	next.DisputeClass = DisputeNotAsDescribed
	return Outcome{Pocket: next, Effects: p.disputeNotify()}, nil
}

// #13 DISPUTED → REFUNDED when the vendor concedes.
func (p Pocket) vendorConcede() (Outcome, error) {
	if p.State != StateDisputed {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateRefunded
	return Outcome{Pocket: next, Effects: p.refundEffects()}, nil
}

// #14 DISPUTED → REFUNDED by admin, flagging the vendor for fraud.
func (p Pocket) adminForceRefund() (Outcome, error) {
	if p.State != StateDisputed {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateRefunded
	effects := p.refundEffects()
	effects = append(effects, Sanction{Role: RoleVendor, Kind: SanctionFraudFlag})
	return Outcome{Pocket: next, Effects: effects}, nil
}

// #15 DISPUTED → SETTLED by admin, optionally striking a bad-faith buyer.
func (p Pocket) adminForcePayout(ev Event) (Outcome, error) {
	if p.State != StateDisputed {
		return Outcome{}, ErrIllegalTransition
	}
	next := p
	next.State = StateSettled
	effects := p.settleEffects()
	if ev.BadFaith {
		effects = append(effects, Sanction{Role: RoleBuyer, Kind: SanctionStrike})
	}
	return Outcome{Pocket: next, Effects: effects}, nil
}

// settleEffects builds the payout plan and settlement notifications, including a
// broker notification for brokered pockets.
func (p Pocket) settleEffects() []Effect {
	effects := []Effect{
		SchedulePayout{Legs: p.PayoutLegs()},
		Notify{Role: RoleVendor, Kind: NotifySettled},
	}
	if p.Structure == StructureBrokered {
		effects = append(effects, Notify{Role: RoleBroker, Kind: NotifySettled})
	}
	return effects
}

// refundEffects returns the full buyer refund plus both-party notifications.
func (p Pocket) refundEffects() []Effect {
	return append(
		[]Effect{ExecuteRefund{BeneficiaryRole: RoleBuyer, AmountKobo: p.BuyerTotalKobo()}},
		bothNotify(NotifyRefundIssued)...,
	)
}

func bothNotify(kind string) []Effect {
	return []Effect{
		Notify{Role: RoleBuyer, Kind: kind},
		Notify{Role: RoleVendor, Kind: kind},
	}
}

// disputeNotify notifies both principals a dispute opened, plus the broker as a
// read-only observer on a brokered pocket.
func (p Pocket) disputeNotify() []Effect {
	effects := bothNotify(NotifyDisputeOpened)
	if p.Structure == StructureBrokered {
		effects = append(effects, Notify{Role: RoleBroker, Kind: NotifyDisputeOpened})
	}
	return effects
}
