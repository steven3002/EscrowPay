package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"escrowpay/internal/auth"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/store"
)

// Authorization model. An account session (cookie) is the caller's identity; a
// link token is a share-link invitation. They compose per participant seat:
//
//   - A seat already claimed by a user is reachable only by that user's
//     session. The link that invited them stops being a credential the moment
//     the seat is bound, so a forwarded or leaked link exposes nothing.
//   - An unclaimed seat is reachable through its link token: the invitee can
//     read the terms before signing in, and claiming the seat requires both
//     the token (which names the seat) and a session (which binds it).
//
// authParticipant applies that model and returns the caller's role.
func (a *API) authParticipant(r *http.Request, pocketID string, parts []store.ParticipantRecord) (string, error) {
	user, hasUser := a.currentUser(r)
	if hasUser {
		for _, p := range parts {
			if p.UserID == user.ID {
				return p.Role, nil
			}
		}
	}

	token := extractToken(r)
	if token == "" {
		if hasUser {
			return "", errForbidden
		}
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
		if p.Role != claims.Role {
			continue
		}
		if p.LinkTokenHash == "" || !linktoken.EqualHash(p.LinkTokenHash, tokenHash) {
			return "", errForbidden
		}
		if p.UserID != "" {
			// The seat is bound to an account; the invitation alone no
			// longer authorizes access to it.
			if !hasUser {
				return "", errUnauthorized
			}
			return "", errForbidden
		}
		return p.Role, nil
	}
	return "", errForbidden
}

// inviteRole resolves which seat a link token invites the caller to take,
// independently of any seat the caller already holds. It powers the claim
// endpoint: an unclaimed seat (or one already bound to this user) resolves to
// its role; a seat bound to another account is forbidden.
func (a *API) inviteRole(r *http.Request, pocketID, userID string, parts []store.ParticipantRecord) (string, error) {
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
		if p.Role != claims.Role {
			continue
		}
		if p.LinkTokenHash == "" || !linktoken.EqualHash(p.LinkTokenHash, tokenHash) {
			return "", errForbidden
		}
		if p.UserID != "" && p.UserID != userID {
			return "", errForbidden
		}
		return p.Role, nil
	}
	return "", errForbidden
}

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

// currentUser resolves the request's session cookie to its account.
func (a *API) currentUser(r *http.Request) (store.UserRecord, bool) {
	if a.auth == nil {
		return store.UserRecord{}, false
	}
	u, err := a.auth.UserFromRequest(r)
	if err != nil {
		if !errors.Is(err, auth.ErrNoSession) {
			a.logger.Error("session lookup failed", "error", err.Error())
		}
		return store.UserRecord{}, false
	}
	return u, true
}

// requireUser resolves the session or writes a 401, for endpoints that act on
// behalf of an account rather than a pocket seat.
func (a *API) requireUser(w http.ResponseWriter, r *http.Request) (store.UserRecord, bool) {
	u, ok := a.currentUser(r)
	if !ok {
		a.writeError(w, errNoAccount)
		return store.UserRecord{}, false
	}
	return u, true
}

// requireAdmin resolves the session and demands the admin flag. The admin
// surface is never open without an admin account, sandbox mode included.
func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) (store.UserRecord, bool) {
	u, ok := a.currentUser(r)
	if !ok {
		a.writeError(w, errNoAccount)
		return store.UserRecord{}, false
	}
	if !u.IsAdmin {
		a.writeError(w, errForbidden)
		return store.UserRecord{}, false
	}
	return u, true
}

const linkTokenHeader = "X-Link-Token"

var (
	// errUnauthorized signals a request with no usable credential: no session
	// and no (valid) link token.
	errUnauthorized = errors.New("unauthorized")
	// errNoAccount signals an endpoint that requires a signed-in account.
	errNoAccount = errors.New("account required")
	// errForbidden signals a credential that verifies but does not authorize the
	// request (wrong pocket, wrong account, or no matching participant).
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

// clientIP is the rate-limit key for a request. Behind the app's own proxy the
// leftmost X-Forwarded-For entry is the caller; without a trusted proxy the
// TCP peer address is authoritative.
func (a *API) clientIP(r *http.Request) string {
	if a.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
