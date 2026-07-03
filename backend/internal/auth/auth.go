// Package auth owns account authentication: server-side sessions carried by an
// HttpOnly cookie, and the Google OIDC sign-in flow. It issues and resolves
// identities; what an identity may do to a pocket remains the transport and
// application layers' concern (per-pocket roles, link tokens).
//
// A session token is 256 bits from crypto/rand, delivered only in the cookie;
// the database stores its SHA-256, so neither a database dump nor a log line
// can impersonate a live session.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"escrowpay/internal/store"
)

// CookieName is the session cookie. SameSite=Lax keeps it off cross-site
// subresource and cross-site POST requests, which together with the Origin
// check in the transport layer is the CSRF defence.
const CookieName = "escrowpay_session"

// ErrNoSession is returned when a request carries no valid session.
var ErrNoSession = errors.New("auth: no valid session")

// SessionStore is the persistence the Manager needs; implemented by *store.Store.
type SessionStore interface {
	InsertSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time, ip, userAgent string) error
	SessionUser(ctx context.Context, tokenHash string, now time.Time) (store.UserRecord, error)
	RevokeSession(ctx context.Context, tokenHash string) error
}

// Manager issues, resolves and revokes sessions.
type Manager struct {
	store  SessionStore
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

// NewManager builds a session manager. secure controls the cookie's Secure
// attribute and must be true wherever the app is served over HTTPS.
func NewManager(s SessionStore, ttl time.Duration, secure bool, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &Manager{store: s, ttl: ttl, secure: secure, now: now}
}

// Issue creates a session for the user and sets its cookie on the response.
func (m *Manager) Issue(ctx context.Context, w http.ResponseWriter, userID, ip, userAgent string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("mint session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := m.now().Add(m.ttl)
	if err := m.store.InsertSession(ctx, userID, hashToken(token), expires, ip, userAgent); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// UserFromRequest resolves the request's session cookie to its user. It
// returns ErrNoSession for a missing, expired, revoked, or unknown session.
func (m *Manager) UserFromRequest(r *http.Request) (store.UserRecord, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.UserRecord{}, ErrNoSession
	}
	u, err := m.store.SessionUser(r.Context(), hashToken(c.Value), m.now())
	if err != nil {
		if errors.Is(err, store.ErrSessionInvalid) {
			return store.UserRecord{}, ErrNoSession
		}
		return store.UserRecord{}, err
	}
	return u, nil
}

// Revoke invalidates the request's session, if any, and expires its cookie.
func (m *Manager) Revoke(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if err := m.store.RevokeSession(r.Context(), hashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
