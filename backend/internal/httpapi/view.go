package httpapi

import (
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// pocketView is the role-scoped projection of a pocket. Money and counterparty
// fields are populated strictly per the visibility matrix — the buyer sees only
// their total, the vendor only their allocation, the broker the full ledger —
// and what a role may not see is omitted from its JSON, never zeroed. The
// Release Code appears in no view — it is served only by the buyer-only
// endpoint.
type pocketView struct {
	ID           string            `json:"id"`
	ShortCode    string            `json:"short_code"`
	State        string            `json:"state"`
	Structure    string            `json:"structure"`
	Mode         string            `json:"mode"`
	YourRole     string            `json:"your_role"`
	You          viewerView        `json:"you"`
	Item         itemView          `json:"item"`
	Money        moneyView         `json:"money"`
	Counterparty *counterpartyView `json:"counterparty,omitempty"`
	Timers       timersView        `json:"timers"`
	FundingURL   string            `json:"funding_url,omitempty"`
	// FundingCheckout marks funding_url as a live provider checkout the buyer
	// can actually pay; SimulateFunding offers the sandbox funding shortcut.
	// Both are buyer-only affordances of the CREATED state.
	FundingCheckout bool `json:"funding_checkout,omitempty"`
	SimulateFunding bool `json:"simulate_funding,omitempty"`
	// PendingInvites names the counterparty seats still open to a fresh
	// invitation, scoped to what the viewing role may see. It tells the client
	// which invite links are still worth re-sharing; the links themselves live
	// only in the sharer's browser, since the server keeps just their hashes.
	PendingInvites []string  `json:"pending_invites,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// viewerView tells the requesting participant where they stand in the
// pre-acceptance handshake, so the client can show the right next step (claim,
// accept, or wait) without inferring it from state alone.
type viewerView struct {
	Claimed  bool `json:"claimed"`
	Accepted bool `json:"accepted"`
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

// pocketView projects rec for the given role and stamps the deployment's
// funding affordances onto a buyer's CREATED view.
func (a *API) pocketView(rec store.PocketRecord, parts []store.ParticipantRecord, role string) pocketView {
	v := buildPocketView(rec, parts, role)
	if pocket.Role(role) == pocket.RoleBuyer && v.State == string(pocket.StateCreated) {
		v.SimulateFunding = a.app.CanSimulateFunding()
		v.FundingCheckout = a.app.RealGateway() && v.FundingURL != ""
	}
	return v
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
		You:       viewerOf(parts, role),
		Item:      itemView{Description: rec.ItemDescription, Category: rec.Category},
		Timers: timersView{
			DeliveryDeadline: timePtr(p.DeliveryDeadline),
			SettleAfter:      timePtr(p.SettleAfter),
			GraceDeadline:    timePtr(p.GraceDeadline),
			FundingExpiresAt: timePtr(p.FundingExpiresAt),
		},
		CreatedAt: rec.CreatedAt,
	}

	v.Money = roleMoney(p, role)
	switch pocket.Role(role) {
	case pocket.RoleBuyer:
		v.Counterparty = counterpartyOf(parts, sellerRole(p.Structure), false, rec.DeliveryAddress, p.State)
		if p.State == pocket.StateCreated {
			v.FundingURL = rec.FundingLinkURL
		}
	case pocket.RoleVendor:
		v.Counterparty = counterpartyOf(parts, pocket.RoleBuyer, true, rec.DeliveryAddress, p.State)
	}
	v.PendingInvites = pendingInvites(parts, role, p.Structure)
	return v
}

// pendingInvites lists the counterparty seats still open to a fresh invitation,
// scoped to what the viewing role may know about. It powers the "copy the link
// again" affordance: the creator or broker who minted a seat's invitation can
// re-share it until that seat is claimed. A brokered buyer transacts with the
// broker's storefront and must not learn a separate vendor seat exists, so that
// seat is never surfaced to them.
func pendingInvites(parts []store.ParticipantRecord, viewer string, structure pocket.Structure) []string {
	var targets []pocket.Role
	switch pocket.Role(viewer) {
	case pocket.RoleBroker:
		targets = []pocket.Role{pocket.RoleBuyer, pocket.RoleVendor}
	case pocket.RoleVendor:
		targets = []pocket.Role{pocket.RoleBuyer}
	case pocket.RoleBuyer:
		if structure == pocket.StructureP2P {
			targets = []pocket.Role{pocket.RoleVendor}
		}
	}
	var out []string
	for _, role := range targets {
		if seatUnclaimed(parts, role) {
			out = append(out, string(role))
		}
	}
	return out
}

// seatUnclaimed reports whether the named seat exists but no account has taken
// it yet — the window in which its invitation link is still live.
func seatUnclaimed(parts []store.ParticipantRecord, role pocket.Role) bool {
	for _, p := range parts {
		if p.Role == string(role) {
			return p.UserID == ""
		}
	}
	return false
}

// viewerOf reports the requesting participant's claim/accept status.
func viewerOf(parts []store.ParticipantRecord, role string) viewerView {
	for _, p := range parts {
		if p.Role == role {
			return viewerView{Claimed: p.UserID != "", Accepted: p.Accepted}
		}
	}
	return viewerView{}
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
