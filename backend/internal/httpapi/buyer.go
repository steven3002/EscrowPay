package httpapi

import (
	"net/http"

	"escrowpay/internal/pocket"
)

type releaseCodeResponse struct {
	ReleaseCode string `json:"release_code"`
}

// handleReleaseCode returns the plaintext Release Code to the buyer only. This
// is the single endpoint that reveals the code; it appears in no pocket view and
// in no log line.
func (a *API) handleReleaseCode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, parts, err := a.app.Load(r.Context(), id)
	if err != nil {
		a.writeError(w, err)
		return
	}
	role, err := a.authParticipant(r, id, parts)
	if err != nil {
		a.writeError(w, err)
		return
	}
	if role != string(pocket.RoleBuyer) {
		a.writeError(w, errForbidden)
		return
	}
	code, err := a.app.ReleaseCode(r.Context(), id)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releaseCodeResponse{ReleaseCode: code})
}
