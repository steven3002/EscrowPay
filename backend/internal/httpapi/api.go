// Package httpapi is the HTTP transport for the pocket lifecycle. It owns route
// registration, authentication (account sessions and link-token invitations),
// rate limiting, and role-scoped serialization; it delegates every use case to
// pocketapp and holds no business logic of its own.
package httpapi

import (
	"log/slog"
	"net/http"

	"escrowpay/internal/auth"
	"escrowpay/internal/gateway/nomba"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/ratelimit"
	"escrowpay/internal/store"
)

// API is the handler set. Construct it with New and mount it with Register;
// wrap the mux with Middleware for the cross-cutting concerns.
type API struct {
	app          *pocketapp.App
	minter       *linktoken.Minter
	auth         *auth.Manager
	users        *store.Store
	google       *auth.Google
	nombaWebhook *nomba.WebhookVerifier
	logger       *slog.Logger

	flowSecret   []byte
	cookieSecure bool
	trustProxy   bool

	globalLimiter *ratelimit.Limiter
	authLimiter   *ratelimit.Limiter
	writeLimiter  *ratelimit.Limiter
}

// Config carries the API's dependencies and transport policy.
type Config struct {
	App    *pocketapp.App
	Minter *linktoken.Minter
	Auth   *auth.Manager
	Users  *store.Store
	Google *auth.Google
	// NombaWebhook verifies provider payment notifications; nil leaves the
	// webhook endpoint unmounted (mock-gateway deployments).
	NombaWebhook *nomba.WebhookVerifier
	Logger       *slog.Logger

	// FlowSecret signs the transient OIDC flow cookie.
	FlowSecret []byte
	// CookieSecure marks auth cookies Secure; enable wherever HTTPS terminates.
	CookieSecure bool
	// TrustProxy keys rate limits on X-Forwarded-For instead of the TCP peer.
	// Enable only when every request arrives through the app's own proxy.
	TrustProxy bool
	// RateLimit enables the request limiters. Tests disable it for
	// determinism except where the limit itself is under test.
	RateLimit bool
}

// New builds an API from cfg.
func New(cfg Config) *API {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	a := &API{
		app:          cfg.App,
		minter:       cfg.Minter,
		auth:         cfg.Auth,
		users:        cfg.Users,
		google:       cfg.Google,
		nombaWebhook: cfg.NombaWebhook,
		logger:       logger,
		flowSecret:   cfg.FlowSecret,
		cookieSecure: cfg.CookieSecure,
		trustProxy:   cfg.TrustProxy,
	}
	if cfg.RateLimit {
		// Budgets are per client key: a generous global ceiling that only
		// abuse reaches, a tight one on credential endpoints, and a moderate
		// one on state-changing pocket calls.
		a.globalLimiter = ratelimit.New(1200, 120, nil)
		a.authLimiter = ratelimit.New(10, 10, nil)
		a.writeLimiter = ratelimit.New(60, 30, nil)
	}
	return a
}

// Register mounts the v1 routes on mux. Route surfaces:
//
//	auth    — GET  /api/auth/providers | /me · POST /demo | /logout
//	          GET  /api/auth/google/start | /google/callback
//	me      — GET  /api/me/pockets                              (dashboard)
//	create  — POST /api/pockets                                 (session)
//	public  — GET  /api/p/{shortCode}                           (role-scoped view)
//	          POST /api/p/{shortCode}/claim | /accept | /cancel
//	vendor  — POST /api/p/{shortCode}/enter-code                (Release Code)
//	          POST /api/p/{shortCode}/confirm-dispatch-failure  (frozen refund)
//	buyer   — GET  /api/pockets/{id}/release-code               (buyer-only)
//	          POST /api/p/{shortCode}/report-issue              (dispute)
//	          POST /api/p/{shortCode}/attest-non-receipt        (frozen)
//	demo    — POST /api/demo/pockets/{id}/simulate-funding      (sandbox)
//	webhook — POST /api/webhooks/nomba                          (HMAC-signed)
//	admin   — GET  /api/admin/pockets/{id}                      (admin session)
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/providers", a.handleAuthProviders)
	mux.HandleFunc("GET /api/auth/me", a.handleMe)
	mux.HandleFunc("POST /api/auth/demo", a.handleDemoLogin)
	mux.HandleFunc("POST /api/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/auth/google/start", a.handleGoogleStart)
	mux.HandleFunc("GET /api/auth/google/callback", a.handleGoogleCallback)

	mux.HandleFunc("GET /api/me/pockets", a.handleMyPockets)

	mux.HandleFunc("POST /api/pockets", a.limitWrites(a.handleCreate))

	mux.HandleFunc("GET /api/p/{shortCode}", a.handlePublicView)
	mux.HandleFunc("POST /api/p/{shortCode}/claim", a.limitWrites(a.handleClaim))
	mux.HandleFunc("POST /api/p/{shortCode}/accept", a.limitWrites(a.handleAccept))
	mux.HandleFunc("POST /api/p/{shortCode}/cancel", a.limitWrites(a.handleCancel))

	mux.HandleFunc("POST /api/p/{shortCode}/enter-code", a.limitWrites(a.handleEnterCode))
	mux.HandleFunc("POST /api/p/{shortCode}/report-issue", a.limitWrites(a.handleReportIssue))
	mux.HandleFunc("POST /api/p/{shortCode}/confirm-dispatch-failure", a.limitWrites(a.handleConfirmDispatchFailure))
	mux.HandleFunc("POST /api/p/{shortCode}/attest-non-receipt", a.limitWrites(a.handleAttestNonReceipt))

	mux.HandleFunc("POST /api/p/{shortCode}/dispute", a.limitWrites(a.handleOpenDispute))
	mux.HandleFunc("POST /api/p/{shortCode}/concede", a.limitWrites(a.handleConcede))
	mux.HandleFunc("POST /api/p/{shortCode}/evidence", a.limitWrites(a.handleUploadEvidence))
	mux.HandleFunc("GET /api/p/{shortCode}/dispute", a.handleDisputeView)

	mux.HandleFunc("GET /api/pockets/{id}/release-code", a.handleReleaseCode)

	mux.HandleFunc("POST /api/demo/pockets/{id}/simulate-funding", a.handleSimulateFunding)

	mux.HandleFunc("POST /api/webhooks/nomba", a.handleNombaWebhook)

	mux.HandleFunc("GET /api/admin/pockets/{id}", a.handleAdminDetail)
	mux.HandleFunc("GET /api/admin/disputes", a.handleDisputeQueue)
	mux.HandleFunc("POST /api/admin/pockets/{id}/force-refund", a.limitWrites(a.handleForceRefund))
	mux.HandleFunc("POST /api/admin/pockets/{id}/force-payout", a.limitWrites(a.handleForcePayout))
}

// Middleware wraps the mounted routes with the cross-cutting transport
// concerns: response hardening headers, a same-origin check on mutating
// browser requests, and the global per-client rate limit.
func (a *API) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.sameOrigin(r) {
			a.writeError(w, errForbidden)
			return
		}
		if a.globalLimiter != nil && !a.globalLimiter.Allow(a.clientIP(r)) {
			a.writeError(w, errRateLimited)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-site browser mutations: when an Origin header is
// present its host must match the host the request was addressed to. Requests
// without an Origin (same-origin GETs, non-browser clients) pass; the session
// cookie's SameSite=Lax already keeps them off cross-site POSTs.
func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Host
	if a.trustProxy {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
	}
	return originHost(origin) == host
}

func originHost(origin string) string {
	for _, scheme := range []string{"https://", "http://"} {
		if len(origin) > len(scheme) && origin[:len(scheme)] == scheme {
			return origin[len(scheme):]
		}
	}
	return origin
}

// limitWrites applies the per-client budget for state-changing endpoints.
func (a *API) limitWrites(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.allow(a.writeLimiter, a.clientIP(r)) {
			a.writeError(w, errRateLimited)
			return
		}
		next(w, r)
	}
}

// allow consults a limiter, treating a nil limiter as unlimited.
func (a *API) allow(l *ratelimit.Limiter, key string) bool {
	return l == nil || l.Allow(key)
}
