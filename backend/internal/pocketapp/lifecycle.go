package pocketapp

import (
	"context"
	"fmt"
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// CreateInput is the terms a creator submits for a new pocket.
type CreateInput struct {
	Structure        pocket.Structure
	CreatorRole      pocket.Role
	Mode             pocket.Mode
	InspectionWindow time.Duration
	DeliveryWindow   time.Duration

	AmountKobo     int64
	CommissionKobo int64
	PremiumKobo    int64

	ItemDescription string
	Category        string

	CreatorUserID string
}

// CreateOutput reports a created draft and the link tokens minted for it. For a
// p2p pocket, Tokens[CounterpartyRole] is the link the creator shares.
type CreateOutput struct {
	PocketID         string
	ShortCode        string
	CreatorRole      pocket.Role
	CounterpartyRole pocket.Role
	Tokens           map[string]string
}

// Create validates the terms and persists a draft pocket with its participant
// rows. The creator is bound and pre-accepted; the counterparty receives a link
// token to claim and accept. The Protection Premium is platform policy: it is
// derived from the goods value here, and any client-submitted value is ignored.
func (a *App) Create(ctx context.Context, in CreateInput) (CreateOutput, error) {
	in.PremiumKobo = pocket.ProtectionPremiumKobo(in.AmountKobo + in.CommissionKobo)
	spec := pocket.Spec{
		Structure:             in.Structure,
		CreatorRole:           in.CreatorRole,
		Mode:                  in.Mode,
		InspectionWindow:      in.InspectionWindow,
		DeliveryWindow:        in.DeliveryWindow,
		AmountKobo:            in.AmountKobo,
		CommissionKobo:        in.CommissionKobo,
		PremiumKobo:           in.PremiumKobo,
		FundingTTL:            a.fundingLinkTTL,
		GracePeriod:           a.gracePeriod,
		EvidenceCaptureWindow: a.evidenceCaptureWindow,
	}
	if err := pocket.ValidateSpec(spec); err != nil {
		return CreateOutput{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.CreatorUserID == "" {
		return CreateOutput{}, fmt.Errorf("%w: creator account is required", ErrInvalidInput)
	}
	if in.ItemDescription == "" {
		return CreateOutput{}, fmt.Errorf("%w: item description is required", ErrInvalidInput)
	}

	draft := store.PocketDraft{
		Structure:        in.Structure,
		CreatorRole:      in.CreatorRole,
		Mode:             in.Mode,
		InspectionWindow: in.InspectionWindow,
		DeliveryWindow:   in.DeliveryWindow,
		AmountKobo:       in.AmountKobo,
		CommissionKobo:   in.CommissionKobo,
		PremiumKobo:      in.PremiumKobo,
		ItemDescription:  in.ItemDescription,
		Category:         in.Category,
	}
	creator := store.CreatorInput{Role: in.CreatorRole, UserID: in.CreatorUserID}

	res, err := a.store.CreatePocket(ctx, draft, creator, participantRoles(in.Structure), a.now(), a.minter.Mint)
	if err != nil {
		return CreateOutput{}, err
	}
	return CreateOutput{
		PocketID:         res.PocketID,
		ShortCode:        res.ShortCode,
		CreatorRole:      in.CreatorRole,
		CounterpartyRole: counterpartyRole(in.Structure, in.CreatorRole),
		Tokens:           res.Tokens,
	}, nil
}

// Claim binds the caller's account to a role on a draft pocket.
func (a *App) Claim(ctx context.Context, pocketID string, role pocket.Role, userID string) error {
	if userID == "" {
		return fmt.Errorf("%w: an account is required to claim", ErrInvalidInput)
	}
	return a.store.Claim(ctx, pocketID, string(role), userID)
}

// ConvertOutput reports a p2p → brokered conversion: the raw link tokens for
// the broker's own seat and the vendor's fresh invitation.
type ConvertOutput struct {
	PocketID string
	Tokens   map[string]string
}

// ConvertToBrokered lets the recipient of a buyer-created vendor invitation
// take the pocket as a middleman instead: the caller becomes the broker, the
// item amount is split into the vendor's allocation plus the broker's
// commission, and a fresh vendor invitation is minted for the real seller.
// The buyer's total never changes — the commission comes out of the price the
// buyer already agreed to — and the Protection Premium is untouched because
// the goods value it prices is the same. The pocket remains a draft until the
// real vendor claims and accepts.
func (a *App) ConvertToBrokered(ctx context.Context, pocketID, userID string, vendorAmountKobo int64) (ConvertOutput, error) {
	if userID == "" {
		return ConvertOutput{}, fmt.Errorf("%w: an account is required to broker a pocket", ErrInvalidInput)
	}
	if vendorAmountKobo <= 0 {
		return ConvertOutput{}, fmt.Errorf("%w: vendor allocation must be positive", ErrInvalidInput)
	}
	res, err := a.store.ConvertToBrokered(ctx, pocketID, userID, vendorAmountKobo, a.now(), a.minter.Mint)
	if err != nil {
		return ConvertOutput{}, err
	}
	return ConvertOutput{PocketID: res.PocketID, Tokens: res.Tokens}, nil
}

// Accept records a role's acceptance. When it completes the participant set it
// fires transition #1 and executes the resulting effects (the funding link).
func (a *App) Accept(ctx context.Context, pocketID string, role pocket.Role, deliveryAddress string) (store.AcceptResult, error) {
	res, err := a.store.Accept(ctx, pocketID, string(role), deliveryAddress, string(role), a.now())
	if err != nil {
		return store.AcceptResult{}, err
	}
	if res.Completed {
		a.applyOutcome(ctx, res.PocketID, res.Outcome)
	}
	return res, nil
}

// SimulateFunding drives transition #2 without a provider payment. It is the
// demo stand-in for a real funding webhook, available only while the sandbox
// funding shortcut is enabled.
func (a *App) SimulateFunding(ctx context.Context, pocketID string) (store.WriteResult, error) {
	if !a.CanSimulateFunding() {
		return store.WriteResult{}, ErrForbidden
	}
	res, err := a.store.RunTransition(ctx, pocketID, "system", pocket.Event{Kind: pocket.EvFundingConfirmed}, a.now())
	if err != nil {
		return store.WriteResult{}, err
	}
	a.applyOutcome(ctx, res.PocketID, res.Outcome)
	return res, nil
}

// Cancel terminates a pocket. A FUNDED pocket may only be cancelled (refunded,
// #5) by the vendor; earlier states may be cancelled by either party.
func (a *App) Cancel(ctx context.Context, pocketID string, actorRole pocket.Role) (store.WriteResult, error) {
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return store.WriteResult{}, err
	}
	if rec.Pocket.State == pocket.StateFunded && actorRole != pocket.RoleVendor {
		return store.WriteResult{}, ErrForbidden
	}
	res, err := a.store.Cancel(ctx, pocketID, string(actorRole), a.now())
	if err != nil {
		return store.WriteResult{}, err
	}
	a.applyOutcome(ctx, res.PocketID, res.Outcome)
	return res, nil
}

func counterpartyRole(structure pocket.Structure, creator pocket.Role) pocket.Role {
	if structure != pocket.StructureP2P {
		return "" // a brokered pocket has two counterparties, not one
	}
	if creator == pocket.RoleVendor {
		return pocket.RoleBuyer
	}
	return pocket.RoleVendor
}
