package httpapi

import (
	"fmt"
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

type claimBrokerRequest struct {
	VendorAmountKobo int64 `json:"vendor_amount_kobo"`
}

type claimBrokerResponse struct {
	Pocket pocketView `json:"pocket"`
	// VendorShareURL is the fresh invitation the broker forwards to the real
	// seller; the link the broker received stops being a credential.
	VendorShareURL string            `json:"vendor_share_url"`
	Tokens         map[string]string `json:"tokens"`
}

// handleClaimBroker converts a buyer-created p2p draft into a brokered pocket
// on behalf of a middleman holding the vendor invitation. The caller names the
// vendor's allocation; the remainder of the item amount becomes their
// commission, and the response carries the fresh vendor invitation to forward.
// Only the unclaimed vendor seat of a buyer-created draft converts.
func (a *API) handleClaimBroker(w http.ResponseWriter, r *http.Request) {
	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req claimBrokerRequest
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, err)
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
	if role != string(pocket.RoleVendor) {
		a.writeError(w, errForbidden)
		return
	}
	out, err := a.app.ConvertToBrokered(r.Context(), rec.ID, user.ID, req.VendorAmountKobo)
	if err != nil {
		a.writeError(w, err)
		return
	}
	converted, convertedParts, err := a.app.Load(r.Context(), rec.ID)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claimBrokerResponse{
		Pocket:         a.pocketView(converted, convertedParts, string(pocket.RoleBroker)),
		VendorShareURL: fmt.Sprintf("/p/%s?t=%s", rec.ShortCode, out.Tokens[string(pocket.RoleVendor)]),
		Tokens:         out.Tokens,
	})
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
