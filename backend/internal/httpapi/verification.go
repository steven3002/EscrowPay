package httpapi

import (
	"net/http"

	"escrowpay/internal/pocket"
)

type enterCodeRequest struct {
	Code string `json:"code"`
}

type enterCodeResponse struct {
	Accepted          bool   `json:"accepted"`
	State             string `json:"state"`
	Locked            bool   `json:"locked"`
	AttemptsRemaining int    `json:"attempts_remaining"`
}

// handleEnterCode verifies the Release Code the vendor collected at handoff. A
// correct code advances the pocket (#6/#8, and #11 in instant mode); a wrong one
// reports the remaining attempts, and entry locks after the fifth failure.
func (a *API) handleEnterCode(w http.ResponseWriter, r *http.Request) {
	var req enterCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	res, err := a.app.EnterCode(r.Context(), rec.ID, pocket.Role(role), req.Code)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enterCodeResponse{
		Accepted:          res.Accepted,
		State:             string(res.State),
		Locked:            res.Locked,
		AttemptsRemaining: res.AttemptsRemaining,
	})
}

// handleReportIssue opens a dispute for the buyer within the inspection window
// (#12).
func (a *API) handleReportIssue(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	if _, err := a.app.ReportIssue(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

// handleConfirmDispatchFailure lets the vendor concede a failed delivery on a
// frozen pocket, refunding the buyer immediately (#9).
func (a *API) handleConfirmDispatchFailure(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	if _, err := a.app.ConfirmDispatchFailure(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

// handleAttestNonReceipt records the buyer's non-receipt attestation on a frozen
// pocket, arming the grace-lapse refund (#9).
func (a *API) handleAttestNonReceipt(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	if err := a.app.AttestNonReceipt(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}
