package httpapi

import (
	"net/http"

	"escrowpay/internal/pocketapp"
)

type simulateFundingResponse struct {
	PocketID string `json:"pocket_id"`
	State    string `json:"state"`
	Status   string `json:"status"`
}

// handleSimulateFunding drives transition #2 without a provider payment. It
// stands in for the signed funding webhook and is available only while the
// sandbox funding shortcut is enabled — deployments on a real gateway keep it
// off unless explicitly re-enabled as a demo fallback.
func (a *API) handleSimulateFunding(w http.ResponseWriter, r *http.Request) {
	if !a.app.CanSimulateFunding() {
		a.writeError(w, pocketapp.ErrForbidden)
		return
	}
	id := r.PathValue("id")
	if _, err := a.app.SimulateFunding(r.Context(), id); err != nil {
		a.writeError(w, err)
		return
	}
	rec, _, err := a.app.Load(r.Context(), id)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, simulateFundingResponse{
		PocketID: id,
		State:    string(rec.Pocket.State),
		Status:   "funded",
	})
}
