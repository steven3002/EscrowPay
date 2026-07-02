package httpapi

import (
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// pocketView is the role-scoped projection of a pocket. Money and counterparty
// fields are populated strictly according to the visibility matrix
// (project-flow §5a): what a role may not see is omitted from its JSON, never
// zeroed. The Release Code appears in no view — it is served only by the
// buyer-only endpoint.
type pocketView struct {
	ID           string            `json:"id"`
	ShortCode    string            `json:"short_code"`
	State        string            `json:"state"`
	Structure    string            `json:"structure"`
	Mode         string            `json:"mode"`
	YourRole     string            `json:"your_role"`
	Item         itemView          `json:"item"`
	Money        moneyView         `json:"money"`
	Counterparty *counterpartyView `json:"counterparty,omitempty"`
	Timers       timersView        `json:"timers"`
	FundingURL   string            `json:"funding_url,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type itemView struct {
	Description string `json:"description"`
	Category    string `json:"category"`
}

// moneyView carries only the amounts visible to the requesting role. Hidden
// amounts are nil and omitted by the encoder.
type moneyView struct {
	Currency       string `json:"currency"`
	BuyerTotalKobo *int64 `json:"buyer_total_kobo,omitempty"`
	AmountKobo     *int64 `json:"amount_kobo,omitempty"`
	CommissionKobo *int64 `json:"commission_kobo,omitempty"`
	PremiumKobo    *int64 `json:"premium_kobo,omitempty"`
}

type counterpartyView struct {
	Role            string `json:"role"`
	DisplayName     string `json:"display_name"`
	DeliveryAddress string `json:"delivery_address,omitempty"`
}

type timersView struct {
	DeliveryDeadline *time.Time `json:"delivery_deadline,omitempty"`
	SettleAfter      *time.Time `json:"settle_after,omitempty"`
	GraceDeadline    *time.Time `json:"grace_deadline,omitempty"`
	FundingExpiresAt *time.Time `json:"funding_expires_at,omitempty"`
}

// buildPocketView projects rec for the given role.
func buildPocketView(rec store.PocketRecord, parts []store.ParticipantRecord, role string) pocketView {
	p := rec.Pocket
	v := pocketView{
		ID:        rec.ID,
		ShortCode: rec.ShortCode,
		State:     string(p.State),
		Structure: string(p.Structure),
		Mode:      string(p.Mode),
		YourRole:  role,
		Item:      itemView{Description: rec.ItemDescription, Category: rec.Category},
		Money:     moneyView{Currency: "NGN"},
		Timers: timersView{
			DeliveryDeadline: timePtr(p.DeliveryDeadline),
			SettleAfter:      timePtr(p.SettleAfter),
			GraceDeadline:    timePtr(p.GraceDeadline),
			FundingExpiresAt: timePtr(p.FundingExpiresAt),
		},
		CreatedAt: rec.CreatedAt,
	}

	switch pocket.Role(role) {
	case pocket.RoleBuyer:
		v.Money.BuyerTotalKobo = int64Ptr(p.BuyerTotalKobo())
		v.Counterparty = counterpartyOf(parts, sellerRole(p.Structure), false, rec.DeliveryAddress, p.State)
		if p.State == pocket.StateCreated {
			v.FundingURL = rec.FundingLinkURL
		}
	case pocket.RoleVendor:
		v.Money.AmountKobo = int64Ptr(p.AmountKobo)
		v.Counterparty = counterpartyOf(parts, pocket.RoleBuyer, true, rec.DeliveryAddress, p.State)
	case pocket.RoleBroker:
		// The broker sees the full ledger and both counterparties (s7 UI).
		v.Money.BuyerTotalKobo = int64Ptr(p.BuyerTotalKobo())
		v.Money.AmountKobo = int64Ptr(p.AmountKobo)
		v.Money.CommissionKobo = int64Ptr(p.CommissionKobo)
		v.Money.PremiumKobo = int64Ptr(p.PremiumKobo)
	}
	return v
}

// sellerRole is the role a buyer transacts with: the vendor in a p2p pocket, or
// the broker's storefront in a brokered one.
func sellerRole(structure pocket.Structure) pocket.Role {
	if structure == pocket.StructureBrokered {
		return pocket.RoleBroker
	}
	return pocket.RoleVendor
}

// counterpartyOf builds the counterparty projection. The delivery address is
// revealed to the vendor only once the pocket is funded (they need it to ship).
func counterpartyOf(parts []store.ParticipantRecord, role pocket.Role, includeAddress bool, address string, state pocket.State) *counterpartyView {
	for _, p := range parts {
		if p.Role == string(role) {
			cv := &counterpartyView{Role: p.Role, DisplayName: p.DisplayName}
			if includeAddress && fundedOrLater(state) {
				cv.DeliveryAddress = address
			}
			return cv
		}
	}
	return nil
}

func fundedOrLater(s pocket.State) bool {
	switch s {
	case pocket.StateFunded, pocket.StateDeliveredPending, pocket.StateSettled,
		pocket.StateDisputed, pocket.StateFrozen, pocket.StateRefunded:
		return true
	default:
		return false
	}
}

func int64Ptr(v int64) *int64 { return &v }

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
