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

// handleSimulateFunding drives transition #2 through the mock gateway. It stands
// in for a real funding webhook and is gated by sandbox mode; the production
// payment integration replaces it with signed webhook ingestion.
func (a *API) handleSimulateFunding(w http.ResponseWriter, r *http.Request) {
	if !a.app.Sandbox() {
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
