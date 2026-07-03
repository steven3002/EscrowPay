package httpapi

import (
	"net/http"
	"time"

	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// handleOpenDispute escalates a frozen pocket to arbitration on the buyer's
// behalf (#10).
func (a *API) handleOpenDispute(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	if _, err := a.app.OpenFrozenDispute(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

// handleConcede resolves a dispute in the buyer's favour when the vendor accepts
// fault (#13).
func (a *API) handleConcede(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	if _, err := a.app.Concede(r.Context(), rec.ID, pocket.Role(role)); err != nil {
		a.writeError(w, err)
		return
	}
	a.respondView(w, r, rec.ID, role)
}

type evidenceView struct {
	ID           string     `json:"id"`
	Party        string     `json:"party"`
	Type         string     `json:"type"`
	CapturedAt   time.Time  `json:"captured_at"`
	WithinWindow *bool      `json:"within_window,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
}

// handleUploadEvidence stores a piece of dispute media. The multipart form
// carries the file under "file" and the evidence type under "type".
func (a *API) handleUploadEvidence(w http.ResponseWriter, r *http.Request) {
	rec, role, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.app.EvidenceMaxBytes()+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		a.writeError(w, errBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		a.writeError(w, errBadRequest)
		return
	}
	defer file.Close()

	ev, err := a.app.UploadEvidence(r.Context(), rec.ID, pocket.Role(role), r.FormValue("type"), header.Filename, file)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, evidenceView{
		ID:           ev.ID,
		Party:        ev.Party,
		Type:         ev.Type,
		CapturedAt:   ev.CapturedAt,
		WithinWindow: ev.WithinWindow,
	})
}

type disputeView struct {
	PocketID   string         `json:"pocket_id"`
	Class      string         `json:"class"`
	OpenedBy   string         `json:"opened_by"`
	State      string         `json:"state"`
	Resolution string         `json:"resolution,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Evidence   []evidenceView `json:"evidence"`
}

// handleDisputeView returns the dispute record and its evidence to any
// participant of the pocket.
func (a *API) handleDisputeView(w http.ResponseWriter, r *http.Request) {
	rec, _, ok := a.authByShortCode(w, r)
	if !ok {
		return
	}
	dispute, evidence, err := a.app.DisputeView(r.Context(), rec.ID)
	if err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildDisputeView(dispute, evidence))
}

func buildDisputeView(d store.DisputeRecord, evidence []store.EvidenceRecord) disputeView {
	v := disputeView{
		PocketID:   d.PocketID,
		Class:      d.Class,
		OpenedBy:   d.OpenedBy,
		State:      d.State,
		Resolution: d.Resolution,
		CreatedAt:  d.CreatedAt,
		Evidence:   make([]evidenceView, 0, len(evidence)),
	}
	for _, e := range evidence {
		created := e.CreatedAt
		v.Evidence = append(v.Evidence, evidenceView{
			ID:           e.ID,
			Party:        e.Party,
			Type:         e.Type,
			CapturedAt:   e.CapturedAt,
			WithinWindow: e.WithinWindow,
			CreatedAt:    &created,
		})
	}
	return v
}
