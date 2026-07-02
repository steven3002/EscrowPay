// Package pocket is the escrow domain core: the Pocket aggregate and its
// normative state machine (project-flow §4). It is pure — it performs no I/O and
// imports no database, HTTP, or gateway package. Transitions return the next
// Pocket snapshot plus a list of Effects for later scopes to execute.
package pocket

import (
	"fmt"
	"time"
)

// Default platform policy durations. A pocket may carry its own values; New
// falls back to these when a spec leaves them zero.
const (
	DefaultGracePeriod           = 24 * time.Hour
	DefaultEvidenceCaptureWindow = 60 * time.Minute
)

// Pocket is the escrow aggregate: an immutable snapshot of one transaction's
// state and the terms that govern its transitions. Transition treats the
// receiver as read-only and returns a new snapshot.
type Pocket struct {
	State       State
	Structure   Structure
	CreatorRole Role

	Mode             Mode
	InspectionWindow time.Duration
	DeliveryWindow   time.Duration

	AmountKobo     int64
	CommissionKobo int64
	PremiumKobo    int64

	DeliveryDeadline time.Time
	SettleAfter      time.Time
	GraceDeadline    time.Time
	FundingExpiresAt time.Time

	CodeAttempts int
	CodeLocked   bool

	DisputeClass DisputeClass

	GracePeriod           time.Duration
	EvidenceCaptureWindow time.Duration
}

// Spec is the construction input for a new pocket. Amounts are in kobo.
type Spec struct {
	Structure        Structure
	CreatorRole      Role
	Mode             Mode
	InspectionWindow time.Duration
	DeliveryWindow   time.Duration

	AmountKobo     int64
	CommissionKobo int64
	PremiumKobo    int64

	FundingTTL time.Duration

	GracePeriod           time.Duration
	EvidenceCaptureWindow time.Duration
}

// Outcome is the result of a construction or transition: the resulting Pocket
// snapshot and the effects the caller must execute.
type Outcome struct {
	Pocket  Pocket
	Effects []Effect
}

// New constructs a pocket in CREATED (transition #1). It validates the spec and
// emits a CreateFundingLink effect whose expiry is now + FundingTTL.
func New(now time.Time, spec Spec) (Outcome, error) {
	if err := spec.validate(); err != nil {
		return Outcome{}, err
	}
	grace := spec.GracePeriod
	if grace == 0 {
		grace = DefaultGracePeriod
	}
	capture := spec.EvidenceCaptureWindow
	if capture == 0 {
		capture = DefaultEvidenceCaptureWindow
	}

	p := Pocket{
		State:                 StateCreated,
		Structure:             spec.Structure,
		CreatorRole:           spec.CreatorRole,
		Mode:                  spec.Mode,
		InspectionWindow:      spec.InspectionWindow,
		DeliveryWindow:        spec.DeliveryWindow,
		AmountKobo:            spec.AmountKobo,
		CommissionKobo:        spec.CommissionKobo,
		PremiumKobo:           spec.PremiumKobo,
		FundingExpiresAt:      now.Add(spec.FundingTTL),
		GracePeriod:           grace,
		EvidenceCaptureWindow: capture,
	}
	return Outcome{
		Pocket:  p,
		Effects: []Effect{CreateFundingLink{ExpiresAt: p.FundingExpiresAt}},
	}, nil
}

// BuyerTotalKobo is the amount the buyer funds: vendor allocation plus any
// broker commission plus the protection premium.
func (p Pocket) BuyerTotalKobo() int64 {
	return p.AmountKobo + p.CommissionKobo + p.PremiumKobo
}

// PayoutLegs is the disbursement plan at settlement: the vendor's allocation
// plus, for a brokered pocket with a non-zero commission, the broker's leg. The
// premium is retained by the platform and is not a leg.
func (p Pocket) PayoutLegs() []PayoutLeg {
	legs := []PayoutLeg{{Role: RoleVendor, AmountKobo: p.AmountKobo}}
	if p.Structure == StructureBrokered && p.CommissionKobo > 0 {
		legs = append(legs, PayoutLeg{Role: RoleBroker, AmountKobo: p.CommissionKobo})
	}
	return legs
}

// DueForSettlement reports whether the pocket is a delivered pocket whose
// inspection window has elapsed. Instant-mode pockets are due at the instant of
// handoff, because their window is zero.
func (p Pocket) DueForSettlement(now time.Time) bool {
	return p.State == StateDeliveredPending && !now.Before(p.SettleAfter)
}

func (s Spec) validate() error {
	if s.AmountKobo <= 0 {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidSpec)
	}
	if s.CommissionKobo < 0 || s.PremiumKobo < 0 {
		return fmt.Errorf("%w: commission and premium must be non-negative", ErrInvalidSpec)
	}
	switch s.Structure {
	case StructureP2P:
		if s.CommissionKobo != 0 {
			return fmt.Errorf("%w: p2p pocket cannot carry commission", ErrInvalidSpec)
		}
		if s.CreatorRole != RoleBuyer && s.CreatorRole != RoleVendor {
			return fmt.Errorf("%w: p2p creator must be buyer or vendor", ErrInvalidSpec)
		}
	case StructureBrokered:
		if s.CreatorRole != RoleBroker {
			return fmt.Errorf("%w: brokered creator must be broker", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("%w: unknown structure %q", ErrInvalidSpec, s.Structure)
	}
	switch s.Mode {
	case ModeInstant:
		if s.InspectionWindow != 0 {
			return fmt.Errorf("%w: instant mode requires a zero inspection window", ErrInvalidSpec)
		}
	case ModeCooldown:
		if s.InspectionWindow <= 0 {
			return fmt.Errorf("%w: cooldown mode requires a positive inspection window", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidSpec, s.Mode)
	}
	if s.DeliveryWindow <= 0 {
		return fmt.Errorf("%w: delivery window must be positive", ErrInvalidSpec)
	}
	if s.FundingTTL <= 0 {
		return fmt.Errorf("%w: funding TTL must be positive", ErrInvalidSpec)
	}
	return nil
}
