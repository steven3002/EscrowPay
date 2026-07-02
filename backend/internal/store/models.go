package store

import (
	"time"

	"escrowpay/internal/pocket"
)

// PocketRecord is a pockets row: its stored identity and audit columns plus the
// domain aggregate reconstructed from the remaining columns. Pocket.State may be
// StateDraft, which the domain machine does not accept.
type PocketRecord struct {
	ID              string
	ShortCode       string
	Version         int
	ItemDescription string
	Category        string
	DeliveryAddress string
	FundingLinkRef  string
	FundingLinkURL  string
	ReleaseCodeHash string
	ReleaseCodeEnc  string
	CreatedAt       time.Time

	Pocket pocket.Pocket
}

// IsDraft reports whether the record is still a pre-acceptance draft.
func (r PocketRecord) IsDraft() bool {
	return string(r.Pocket.State) == StateDraft
}

// ParticipantRecord is one pocket_participants row, joined with its user's
// display fields when a user has claimed the role.
type ParticipantRecord struct {
	ID            string
	Role          string
	UserID        string // empty when unclaimed
	DisplayName   string
	Phone         string
	Accepted      bool
	AcceptedAt    time.Time
	LinkTokenHash string
}

// EventRecord is one pocket_events row for the audit timeline.
type EventRecord struct {
	ID        int64
	Actor     string
	FromState string
	ToState   string
	Kind      string
	CreatedAt time.Time
}

// PocketDraft is the input to draft creation: the terms a pocket carries before
// any participant has accepted. Amounts are in kobo.
type PocketDraft struct {
	Structure        pocket.Structure
	CreatorRole      pocket.Role
	Mode             pocket.Mode
	InspectionWindow time.Duration
	DeliveryWindow   time.Duration
	AmountKobo       int64
	CommissionKobo   int64
	PremiumKobo      int64
	ItemDescription  string
	Category         string
}

// specFromRecord rebuilds a pocket.Spec from a draft record, layering in the
// platform policy durations so pocket.New can construct the CREATED aggregate.
func (s *Store) specFromRecord(rec PocketRecord) pocket.Spec {
	return pocket.Spec{
		Structure:             rec.Pocket.Structure,
		CreatorRole:           rec.Pocket.CreatorRole,
		Mode:                  rec.Pocket.Mode,
		InspectionWindow:      rec.Pocket.InspectionWindow,
		DeliveryWindow:        rec.Pocket.DeliveryWindow,
		AmountKobo:            rec.Pocket.AmountKobo,
		CommissionKobo:        rec.Pocket.CommissionKobo,
		PremiumKobo:           rec.Pocket.PremiumKobo,
		FundingTTL:            s.fundingLinkTTL,
		GracePeriod:           s.gracePeriod,
		EvidenceCaptureWindow: s.evidenceCaptureWindow,
	}
}
