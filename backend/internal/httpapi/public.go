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
	writeJSON(w, http.StatusOK, a.pocketView(rec, parts, role))
}

// handleClaim binds the signed-in account to the role the link token names.
// The token is the invitation; the session is the identity that takes the
// seat. Once bound, only that account's session can act on the seat.
func (a *API) handleClaim(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.inviteRole(r, rec.ID, user.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if err := a.app.Claim(r.Context(), rec.ID, pocket.Role(role), user.ID); err != nil {
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
	writeJSON(w, http.StatusOK, a.pocketView(rec, parts, role))
}
