package httpapi

import (
	"net/http"
	"time"

	"escrowpay/internal/store"
)

// adminDetailView is the unscoped, full view of a pocket for the admin dispute
// desk: every money field, both participants, and the complete audit timeline.
// It deliberately omits the Release Code — no surface, admin included, exposes
// the plaintext except the buyer-only endpoint.
type adminDetailView struct {
	ID              string             `json:"id"`
	ShortCode       string             `json:"short_code"`
	State           string             `json:"state"`
	Structure       string             `json:"structure"`
	Mode            string             `json:"mode"`
	Item            itemView           `json:"item"`
	Money           moneyView          `json:"money"`
	DeliveryAddress string             `json:"delivery_address,omitempty"`
	Timers          timersView         `json:"timers"`
	Participants    []adminParticipant `json:"participants"`
	Events          []adminEvent       `json:"events"`
	CreatedAt       time.Time          `json:"created_at"`
}

type adminParticipant struct {
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Claimed     bool   `json:"claimed"`
	Accepted    bool   `json:"accepted"`
}

type adminEvent struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	FromState string    `json:"from_state,omitempty"`
	ToState   string    `json:"to_state,omitempty"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

// handleAdminDetail returns a pocket's full detail and timeline. Admin auth is
// demo-grade: the surface is open only while sandbox mode is on.
func (a *API) handleAdminDetail(w http.ResponseWriter, r *http.Request) {
	if !a.app.Sandbox() {
		a.writeError(w, errForbidden)
		return
	}
	rec, parts, events, err := a.app.Detail(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildAdminDetail(rec, parts, events))
}

func buildAdminDetail(rec store.PocketRecord, parts []store.ParticipantRecord, events []store.EventRecord) adminDetailView {
	p := rec.Pocket
	view := adminDetailView{
		ID:        rec.ID,
		ShortCode: rec.ShortCode,
		State:     string(p.State),
		Structure: string(p.Structure),
		Mode:      string(p.Mode),
		Item:      itemView{Description: rec.ItemDescription, Category: rec.Category},
		Money: moneyView{
			Currency:       "NGN",
			BuyerTotalKobo: int64Ptr(p.BuyerTotalKobo()),
			AmountKobo:     int64Ptr(p.AmountKobo),
			CommissionKobo: int64Ptr(p.CommissionKobo),
			PremiumKobo:    int64Ptr(p.PremiumKobo),
		},
		DeliveryAddress: rec.DeliveryAddress,
		Timers: timersView{
			DeliveryDeadline: timePtr(p.DeliveryDeadline),
			SettleAfter:      timePtr(p.SettleAfter),
			GraceDeadline:    timePtr(p.GraceDeadline),
			FundingExpiresAt: timePtr(p.FundingExpiresAt),
		},
		CreatedAt: rec.CreatedAt,
	}
	for _, part := range parts {
		view.Participants = append(view.Participants, adminParticipant{
			Role:        part.Role,
			DisplayName: part.DisplayName,
			Phone:       part.Phone,
			Claimed:     part.UserID != "",
			Accepted:    part.Accepted,
		})
	}
	for _, e := range events {
		view.Events = append(view.Events, adminEvent{
			ID:        e.ID,
			Actor:     e.Actor,
			FromState: e.FromState,
			ToState:   e.ToState,
			Kind:      e.Kind,
			CreatedAt: e.CreatedAt,
		})
	}
	return view
}
