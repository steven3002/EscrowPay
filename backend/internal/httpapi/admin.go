package httpapi

import (
	"errors"
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
	Dispute         *adminDispute      `json:"dispute,omitempty"`
	Evidence        []evidenceView     `json:"evidence"`
	CreatedAt       time.Time          `json:"created_at"`
}

type adminDispute struct {
	Class      string    `json:"class"`
	OpenedBy   string    `json:"opened_by"`
	State      string    `json:"state"`
	Resolution string    `json:"resolution,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
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

// handleAdminDetail returns a pocket's full detail and timeline. The admin
// surface requires a signed-in admin account in every mode.
func (a *API) handleAdminDetail(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	view, err := a.assembleAdminDetail(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type forcePayoutRequest struct {
	BadFaith bool `json:"bad_faith"`
}

// handleForceRefund is the admin arbitration outcome against the vendor: refund
// the buyer and flag the vendor (#14).
func (a *API) handleForceRefund(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if _, err := a.app.ForceRefund(r.Context(), r.PathValue("id")); err != nil {
		a.writeError(w, err)
		return
	}
	a.writeAdminDetail(w, r, r.PathValue("id"))
}

// handleForcePayout is the admin arbitration outcome against the buyer: release
// to the vendor, optionally striking the buyer (#15).
func (a *API) handleForcePayout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req forcePayoutRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}
	if _, err := a.app.ForcePayout(r.Context(), r.PathValue("id"), req.BadFaith); err != nil {
		a.writeError(w, err)
		return
	}
	a.writeAdminDetail(w, r, r.PathValue("id"))
}

type disputeQueueItem struct {
	PocketID  string    `json:"pocket_id"`
	ShortCode string    `json:"short_code"`
	State     string    `json:"state"`
	Class     string    `json:"class"`
	OpenedBy  string    `json:"opened_by"`
	CreatedAt time.Time `json:"created_at"`
}

// handleDisputeQueue returns the open-dispute arbitration queue.
func (a *API) handleDisputeQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	items, err := a.app.ListDisputes(r.Context())
	if err != nil {
		a.writeError(w, err)
		return
	}
	out := make([]disputeQueueItem, 0, len(items))
	for _, it := range items {
		out = append(out, disputeQueueItem{
			PocketID:  it.PocketID,
			ShortCode: it.ShortCode,
			State:     it.State,
			Class:     it.Class,
			OpenedBy:  it.OpenedBy,
			CreatedAt: it.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"disputes": out})
}

// writeAdminDetail reloads a pocket and writes its full admin detail, so an
// arbitration action returns the resulting state and audit trail.
func (a *API) writeAdminDetail(w http.ResponseWriter, r *http.Request, pocketID string) {
	view, err := a.assembleAdminDetail(r, pocketID)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// assembleAdminDetail gathers a pocket's full arbitration picture: state and
// timeline, all evidence, and the dispute record if one was opened.
func (a *API) assembleAdminDetail(r *http.Request, pocketID string) (adminDetailView, error) {
	rec, parts, events, err := a.app.Detail(r.Context(), pocketID)
	if err != nil {
		return adminDetailView{}, err
	}
	evidence, err := a.app.Evidence(r.Context(), pocketID)
	if err != nil {
		return adminDetailView{}, err
	}
	view := buildAdminDetail(rec, parts, events, evidence)
	if d, err := a.app.Dispute(r.Context(), pocketID); err == nil {
		view.Dispute = &adminDispute{
			Class:      d.Class,
			OpenedBy:   d.OpenedBy,
			State:      d.State,
			Resolution: d.Resolution,
			CreatedAt:  d.CreatedAt,
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return adminDetailView{}, err
	}
	return view, nil
}

func buildAdminDetail(rec store.PocketRecord, parts []store.ParticipantRecord, events []store.EventRecord, evidence []store.EvidenceRecord) adminDetailView {
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
	view.Evidence = make([]evidenceView, 0, len(evidence))
	for _, e := range evidence {
		created := e.CreatedAt
		view.Evidence = append(view.Evidence, evidenceView{
			ID:           e.ID,
			Party:        e.Party,
			Type:         e.Type,
			CapturedAt:   e.CapturedAt,
			WithinWindow: e.WithinWindow,
			CreatedAt:    &created,
		})
	}
	return view
}
