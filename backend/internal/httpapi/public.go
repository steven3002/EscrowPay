package httpapi

import (
	"net/http"

	"escrowpay/internal/pocket"
)

// handlePublicView returns the role-scoped terms view for a shared link.
func (a *API) handlePublicView(w http.ResponseWriter, r *http.Request) {
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.authParticipant(r, rec.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildPocketView(rec, parts, role))
}

type claimRequest struct {
	Phone       string `json:"phone"`
	DisplayName string `json:"display_name"`
}

// handleClaim binds the caller to their role on a draft pocket.
func (a *API) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.authParticipant(r, rec.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if err := a.app.Claim(r.Context(), rec.ID, pocket.Role(role), req.Phone, req.DisplayName); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

type acceptRequest struct {
	DeliveryAddress string `json:"delivery_address"`
}

// handleAccept records the caller's acceptance, which may fire transition #1.
func (a *API) handleAccept(w http.ResponseWriter, r *http.Request) {
	var req acceptRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.authParticipant(r, rec.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if _, err := a.app.Accept(r.Context(), rec.ID, pocket.Role(role), req.DeliveryAddress); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

// handleCancel terminates a pocket on behalf of the caller's role.
func (a *API) handleCancel(w http.ResponseWriter, r *http.Request) {
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.authParticipant(r, rec.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if _, err := a.app.Cancel(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

// respondView reloads a pocket and writes its role-scoped view, so a mutating
// call returns the resulting state.
func (a *API) respondView(w http.ResponseWriter, r *http.Request, pocketID, role string) {
	rec, parts, err := a.app.Load(r.Context(), pocketID)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildPocketView(rec, parts, role))
}
