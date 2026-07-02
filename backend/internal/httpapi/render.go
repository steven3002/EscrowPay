package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"escrowpay/internal/pocket"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/store"
)

// writeJSON serializes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

type errorBody struct {
	Error string `json:"error"`
}

// writeError maps a domain/application/store error to an HTTP status and a
// non-leaky message. Unmapped errors are 500 and are logged in full.
func (a *API) writeError(w http.ResponseWriter, err error) {
	status, msg := classify(err)
	if status >= 500 {
		a.logger.Error("request failed", slog.String("error", err.Error()))
	}
	writeJSON(w, status, errorBody{Error: msg})
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, errUnauthorized):
		return http.StatusUnauthorized, "missing or invalid link token"
	case errors.Is(err, errForbidden), errors.Is(err, pocketapp.ErrForbidden):
		return http.StatusForbidden, "not permitted for this role"
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, pocketapp.ErrInvalidInput), errors.Is(err, pocket.ErrInvalidSpec), errors.Is(err, errBadRequest):
		return http.StatusBadRequest, messageOf(err)
	case errors.Is(err, store.ErrConflict),
		errors.Is(err, store.ErrIllegalState),
		errors.Is(err, store.ErrAlreadyAccepted),
		errors.Is(err, store.ErrAlreadyClaimed),
		errors.Is(err, store.ErrNotClaimed),
		errors.Is(err, pocket.ErrIllegalTransition),
		errors.Is(err, pocket.ErrTerminal),
		errors.Is(err, pocketapp.ErrCodeNotReady):
		return http.StatusConflict, messageOf(err)
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// messageOf returns a client-safe message for expected 4xx errors.
func messageOf(err error) string {
	return err.Error()
}

// errBadRequest tags request-decoding failures for classify.
var errBadRequest = errors.New("bad request")

// decodeJSON reads a required JSON body into dst, returning errBadRequest on
// failure (including an empty body).
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errBadRequest
	}
	return nil
}

// decodeOptionalJSON reads a JSON body into dst when one is present, treating an
// empty body as an empty object rather than an error.
func decodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return errBadRequest
	}
	return nil
}

// defaultStr returns v, or fallback when v is empty.
func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
