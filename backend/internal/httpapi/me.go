package httpapi

import (
	"net/http"
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// pocketSummary is one dashboard row: the pocket as seen by the requesting
// user's role in it. Money fields obey the same visibility matrix as the full
// pocket view — the summary is built from the same role-scoped projection.
type pocketSummary struct {
	ID           string     `json:"id"`
	ShortCode    string     `json:"short_code"`
	State        string     `json:"state"`
	Structure    string     `json:"structure"`
	Mode         string     `json:"mode"`
	Role         string     `json:"role"`
	Active       bool       `json:"active"`
	Item         itemView   `json:"item"`
	Money        moneyView  `json:"money"`
	Counterparty string     `json:"counterparty,omitempty"`
	Timers       timersView `json:"timers"`
	CreatedAt    time.Time  `json:"created_at"`
}

// handleMyPockets returns every pocket the signed-in user participates in,
// newest first — the cross-role dashboard feed (buyer, vendor and broker seats
// alike).
func (a *API) handleMyPockets(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	ups, err := a.app.MyPockets(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, err)
		return
	}
	summaries := make([]pocketSummary, 0, len(ups))
	for _, up := range ups {
		summaries = append(summaries, buildPocketSummary(up.Record, up.Role))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pockets": summaries})
}

// buildPocketSummary projects one pocket for the dashboard. A pocket is active
// until it reaches a terminal state; drafts count as active because they await
// action.
func buildPocketSummary(rec store.PocketRecord, role string) pocketSummary {
	p := rec.Pocket
	return pocketSummary{
		ID:        rec.ID,
		ShortCode: rec.ShortCode,
		State:     string(p.State),
		Structure: string(p.Structure),
		Mode:      string(p.Mode),
		Role:      role,
		Active:    !p.State.IsTerminal(),
		Item:      itemView{Description: rec.ItemDescription, Category: rec.Category},
		Money:     roleMoney(p, role),
		Timers: timersView{
			DeliveryDeadline: timePtr(p.DeliveryDeadline),
			SettleAfter:      timePtr(p.SettleAfter),
			GraceDeadline:    timePtr(p.GraceDeadline),
			FundingExpiresAt: timePtr(p.FundingExpiresAt),
		},
		CreatedAt: rec.CreatedAt,
	}
}

// roleMoney is the visibility matrix for money fields, shared by the pocket
// view and the dashboard summary: buyer sees their total, vendor their
// allocation, broker the full ledger.
func roleMoney(p pocket.Pocket, role string) moneyView {
	m := moneyView{Currency: "NGN"}
	switch pocket.Role(role) {
	case pocket.RoleBuyer:
		m.BuyerTotalKobo = int64Ptr(p.BuyerTotalKobo())
	case pocket.RoleVendor:
		m.AmountKobo = int64Ptr(p.AmountKobo)
	case pocket.RoleBroker:
		m.BuyerTotalKobo = int64Ptr(p.BuyerTotalKobo())
		m.AmountKobo = int64Ptr(p.AmountKobo)
		m.CommissionKobo = int64Ptr(p.CommissionKobo)
		m.PremiumKobo = int64Ptr(p.PremiumKobo)
	}
	return m
}
