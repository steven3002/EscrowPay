package httpapi

import (
	"errors"
	"net/http"

	"escrowpay/internal/linktoken"
	"escrowpay/internal/store"
)

// authByShortCode loads the pocket named by the {shortCode} path value and
// authenticates the caller against its participants. On any failure it writes the
// mapped error response and returns ok=false, so a handler can early-return. It
// centralizes the load-then-authenticate preamble the mutating endpoints share.
func (a *API) authByShortCode(w http.ResponseWriter, r *http.Request) (store.PocketRecord, string, bool) {
	rec, parts, err := a.app.LoadByShortCode(r.Context(), r.PathValue("shortCode"))
	if err != nil {
		a.writeError(w, err)
		return store.PocketRecord{}, "", false
	}
	role, err := a.authParticipant(r, rec.ID, parts)
	if err != nil {
		a.writeError(w, err)
		return store.PocketRecord{}, "", false
	}
	return rec, role, true
}

const linkTokenHeader = "X-Link-Token"

var (
	// errUnauthorized signals a missing or unverifiable link token.
	errUnauthorized = errors.New("unauthorized")
	// errForbidden signals a token that verifies but does not authorize the
	// request (wrong pocket, or no matching participant).
	errForbidden = errors.New("forbidden")
)

// extractToken reads the link token from the header, falling back to a query
// parameter so a shared browser link works without custom headers.
func extractToken(r *http.Request) string {
	if t := r.Header.Get(linkTokenHeader); t != "" {
		return t
	}
	return r.URL.Query().Get("t")
}

// authParticipant authenticates the request against a pocket's participants. It
// verifies the token signature, confirms it names this pocket, and binds it to
// exactly one participant row by matching the stored token hash in constant
// time. It returns the caller's role.
func (a *API) authParticipant(r *http.Request, pocketID string, parts []store.ParticipantRecord) (string, error) {
	token := extractToken(r)
	if token == "" {
		return "", errUnauthorized
	}
	claims, err := a.minter.Parse(token)
	if err != nil {
		return "", errUnauthorized
	}
	if claims.PocketID != pocketID {
		return "", errForbidden
	}
	tokenHash := linktoken.Hash(token)
	for _, p := range parts {
		if p.Role == claims.Role {
			if p.LinkTokenHash != "" && linktoken.EqualHash(p.LinkTokenHash, tokenHash) {
				return p.Role, nil
			}
			return "", errForbidden
		}
	}
	return "", errForbidden
}
