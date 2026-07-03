package pocket

import "time"

// Effect is a side effect a transition asks the caller to perform. Effects are
// inert data: the domain never executes them. The outer layers (persistence,
// HTTP, settlement) interpret each Effect and drive the gateway, notifier, or
// store.
type Effect interface {
	isEffect()
}

// PayoutLeg is one beneficiary's share of a settlement. A p2p settlement has a
// single vendor leg; a brokered settlement adds a broker commission leg.
type PayoutLeg struct {
	Role       Role
	AmountKobo int64
}

// CreateFundingLink asks the gateway to mint the checkout artifact a buyer
// funds. Emitted by New (transition #1).
type CreateFundingLink struct {
	ExpiresAt time.Time
}

// GenerateReleaseCode asks the caller to mint a Release Code, persist its hash,
// and reveal the plaintext to the buyer only. Emitted at funding (#2).
type GenerateReleaseCode struct{}

// PromptEvidence asks the caller to prompt the buyer to record an in-app
// unboxing video within CaptureWindow. Emitted on Cooldown-mode handoff (#6/#8).
type PromptEvidence struct {
	CaptureWindow time.Duration
}

// StartGrace signals that the delivery window closed and a grace period is now
// running until Until. Emitted on freeze (#7).
type StartGrace struct {
	Until time.Time
}

// SchedulePayout asks the caller to disburse each leg through the gateway, one
// idempotent call per leg. Emitted on settlement (#11, #15).
type SchedulePayout struct {
	Legs []PayoutLeg
}

// ExecuteRefund asks the caller to return AmountKobo to the beneficiary through
// the gateway. Emitted on every refund (#5, #9, #13, #14).
type ExecuteRefund struct {
	BeneficiaryRole Role
	AmountKobo      int64
}

// Notify asks the caller to deliver a message of the given Kind to the
// participant in the given Role.
type Notify struct {
	Role Role
	Kind string
}

// Sanction asks the caller to record an enforcement action against a
// participant. Emitted on adverse dispute outcomes (#14, #15).
type Sanction struct {
	Role Role
	Kind SanctionKind
}

func (CreateFundingLink) isEffect()   {}
func (GenerateReleaseCode) isEffect() {}
func (PromptEvidence) isEffect()      {}
func (StartGrace) isEffect()          {}
func (SchedulePayout) isEffect()      {}
func (ExecuteRefund) isEffect()       {}
func (Notify) isEffect()              {}
func (Sanction) isEffect()            {}
