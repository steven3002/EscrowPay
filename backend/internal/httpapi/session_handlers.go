package httpapi

import (
	"net/http"
	"strings"
	"time"

	"escrowpay/internal/auth"
	"escrowpay/internal/store"
)

// oauthFlowCookie carries the signed state of one in-flight Google sign-in
// between the start redirect and the callback.
const oauthFlowCookie = "escrowpay_oauth"

// flowTTL bounds how long a started sign-in may take before it must restart.
const flowTTL = 10 * time.Minute

type userView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone,omitempty"`
	Email       string `json:"email,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	IsAdmin     bool   `json:"is_admin"`
	TrustTier   int    `json:"trust_tier"`
	Strikes     int    `json:"strikes"`
}

func buildUserView(u store.UserRecord) userView {
	return userView{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		Phone:       u.Phone,
		Email:       u.Email,
		AvatarURL:   u.AvatarURL,
		IsAdmin:     u.IsAdmin,
		TrustTier:   u.TrustTier,
		Strikes:     u.Strikes,
	}
}

// handleAuthProviders reports which sign-in methods this deployment offers, so
// the client renders only usable buttons.
func (a *API) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"google": a.google != nil,
		"demo":   a.app.Sandbox(),
	})
}

// handleMe returns the signed-in account, or 401 when there is none.
func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": buildUserView(u)})
}

// handleLogout revokes the current session. It is idempotent.
func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := a.auth.Revoke(w, r); err != nil {
		a.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type demoLoginRequest struct {
	Phone       string `json:"phone"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// handleDemoLogin signs in (creating if needed) a demo account keyed by phone.
// It exists so the sandbox demo can switch actors without an identity
// provider, and is disabled outside sandbox mode exactly like simulate-funding.
// The admin flag is honoured only here: production admin accounts are
// provisioned out of band.
func (a *API) handleDemoLogin(w http.ResponseWriter, r *http.Request) {
	if !a.app.Sandbox() {
		a.writeError(w, errForbidden)
		return
	}
	if !a.allow(a.authLimiter, a.clientIP(r)) {
		a.writeError(w, errRateLimited)
		return
	}
	var req demoLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, err)
		return
	}
	req.Phone = strings.TrimSpace(req.Phone)
	if req.Phone == "" {
		a.writeError(w, errBadRequest)
		return
	}
	u, err := a.users.UpsertUserByPhone(r.Context(), req.Phone, strings.TrimSpace(req.DisplayName))
	if err != nil {
		a.writeError(w, err)
		return
	}
	if req.Admin && !u.IsAdmin {
		if err := a.users.SetAdmin(r.Context(), u.ID, true); err != nil {
			a.writeError(w, err)
			return
		}
		u.IsAdmin = true
	}
	if err := a.auth.Issue(r.Context(), w, u.ID, a.clientIP(r), r.UserAgent()); err != nil {
		a.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": buildUserView(u)})
}

// handleGoogleStart begins the OIDC sign-in: it binds state, nonce and a PKCE
// verifier to a signed short-lived cookie and redirects to the issuer.
func (a *API) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		a.writeError(w, errAuthUnavailable)
		return
	}
	if !a.allow(a.authLimiter, a.clientIP(r)) {
		a.writeError(w, errRateLimited)
		return
	}
	fs, challenge, err := auth.NewFlowState(safeNextPath(r.URL.Query().Get("next")), flowTTL, time.Now())
	if err != nil {
		a.writeError(w, err)
		return
	}
	encoded, err := fs.Encode(a.flowSecret)
	if err != nil {
		a.writeError(w, err)
		return
	}
	authURL, err := a.google.AuthCodeURL(r.Context(), fs.State, fs.Nonce, challenge)
	if err != nil {
		a.writeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthFlowCookie,
		Value:    encoded,
		Path:     "/api/auth/google",
		MaxAge:   int(flowTTL.Seconds()),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleGoogleCallback completes the OIDC sign-in: state check, code exchange
// (PKCE), local ID-token verification, account upsert, session issue. Being a
// browser navigation, failures redirect to the login screen rather than
// returning JSON.
func (a *API) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		a.writeError(w, errAuthUnavailable)
		return
	}
	clearFlow := func() {
		http.SetCookie(w, &http.Cookie{
			Name: oauthFlowCookie, Value: "", Path: "/api/auth/google", MaxAge: -1,
			HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteLaxMode,
		})
	}
	fail := func(reason string, err error) {
		clearFlow()
		a.logger.Warn("google sign-in failed", "reason", reason, "error", errString(err))
		http.Redirect(w, r, "/login?error="+reason, http.StatusSeeOther)
	}

	c, err := r.Cookie(oauthFlowCookie)
	if err != nil || c.Value == "" {
		fail("flow", nil)
		return
	}
	fs, err := auth.DecodeFlowState(a.flowSecret, c.Value, time.Now())
	if err != nil {
		fail("flow", err)
		return
	}
	if state := r.URL.Query().Get("state"); state == "" || state != fs.State {
		fail("state", nil)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("denied", nil)
		return
	}
	identity, err := a.google.Exchange(r.Context(), code, fs.Verifier, fs.Nonce)
	if err != nil {
		fail("verify", err)
		return
	}
	email := ""
	if identity.EmailVerified {
		email = identity.Email
	}
	u, err := a.users.UpsertUserByGoogle(r.Context(), identity.Sub, email, identity.Name, identity.Picture)
	if err != nil {
		fail("account", err)
		return
	}
	if err := a.auth.Issue(r.Context(), w, u.ID, a.clientIP(r), r.UserAgent()); err != nil {
		fail("session", err)
		return
	}
	clearFlow()
	http.Redirect(w, r, fs.Next, http.StatusSeeOther)
}

// safeNextPath admits only same-site absolute paths as post-login targets,
// defaulting to the dashboard. This closes the open-redirect hole a raw next
// parameter would be.
func safeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/dashboard"
	}
	return next
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
