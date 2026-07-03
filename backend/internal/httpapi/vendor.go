package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/pocketapp"
)

type createRequest struct {
	Structure               string `json:"structure"`
	CreatorRole             string `json:"creator_role"`
	Mode                    string `json:"mode"`
	InspectionWindowMinutes int    `json:"inspection_window_minutes"`
	DeliveryWindowMinutes   int    `json:"delivery_window_minutes"`
	AmountKobo              int64  `json:"amount_kobo"`
	CommissionKobo          int64  `json:"commission_kobo"`
	PremiumKobo             int64  `json:"premium_kobo"`
	ItemDescription         string `json:"item_description"`
	Category                string `json:"category"`
}

type createResponse struct {
	PocketID         string            `json:"pocket_id"`
	ShortCode        string            `json:"short_code"`
	CreatorRole      string            `json:"creator_role"`
	CounterpartyRole string            `json:"counterparty_role,omitempty"`
	ShareURL         string            `json:"share_url,omitempty"`
	Tokens           map[string]string `json:"tokens"`
}

// handleCreate creates a draft pocket authored by the signed-in account. It is
// initiator-agnostic: creator_role selects who authored it. The response
// carries a link token per role; the counterparty's is the one to share.
func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}

	in := pocketapp.CreateInput{
		Structure:        pocket.Structure(defaultStr(req.Structure, string(pocket.StructureP2P))),
		CreatorRole:      pocket.Role(req.CreatorRole),
		Mode:             pocket.Mode(req.Mode),
		InspectionWindow: time.Duration(req.InspectionWindowMinutes) * time.Minute,
		DeliveryWindow:   time.Duration(req.DeliveryWindowMinutes) * time.Minute,
		AmountKobo:       req.AmountKobo,
		CommissionKobo:   req.CommissionKobo,
		PremiumKobo:      req.PremiumKobo,
		ItemDescription:  req.ItemDescription,
		Category:         defaultStr(req.Category, "general"),
		CreatorUserID:    user.ID,
	}

	out, err := a.app.Create(r.Context(), in)
	if err != nil {
		a.writeError(w, err)
		return
	}

	resp := createResponse{
		PocketID:         out.PocketID,
		ShortCode:        out.ShortCode,
		CreatorRole:      string(out.CreatorRole),
		CounterpartyRole: string(out.CounterpartyRole),
		Tokens:           out.Tokens,
	}
	if cp := string(out.CounterpartyRole); cp != "" {
		if tok := out.Tokens[cp]; tok != "" {
			resp.ShareURL = fmt.Sprintf("/p/%s?t=%s", out.ShortCode, tok)
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}
