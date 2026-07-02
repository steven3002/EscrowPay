// Package httpapi is the HTTP transport for the pocket lifecycle. It owns route
// registration, link-token authentication, and role-scoped serialization; it
// delegates every use case to pocketapp and holds no business logic of its own.
package httpapi

import (
	"log/slog"
	"net/http"

	"escrowpay/internal/linktoken"
	"escrowpay/internal/pocketapp"
)

// API is the handler set. Construct it with New and mount it with Register.
type API struct {
	app    *pocketapp.App
	minter *linktoken.Minter
	logger *slog.Logger
}

// New builds an API over the application service and token minter.
func New(app *pocketapp.App, minter *linktoken.Minter, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{app: app, minter: minter, logger: logger}
}

// Register mounts the v1 routes on mux. Route surfaces:
//
//	vendor  — POST /api/pockets                                 (create)
//	public  — GET  /api/p/{shortCode}                           (role-scoped view)
//	          POST /api/p/{shortCode}/claim | /accept | /cancel
//	buyer   — GET  /api/pockets/{id}/release-code               (buyer-only)
//	demo    — POST /api/demo/pockets/{id}/simulate-funding      (sandbox)
//	admin   — GET  /api/admin/pockets/{id}                      (full detail)
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pockets", a.handleCreate)

	mux.HandleFunc("GET /api/p/{shortCode}", a.handlePublicView)
	mux.HandleFunc("POST /api/p/{shortCode}/claim", a.handleClaim)
	mux.HandleFunc("POST /api/p/{shortCode}/accept", a.handleAccept)
	mux.HandleFunc("POST /api/p/{shortCode}/cancel", a.handleCancel)

	mux.HandleFunc("GET /api/pockets/{id}/release-code", a.handleReleaseCode)

	mux.HandleFunc("POST /api/demo/pockets/{id}/simulate-funding", a.handleSimulateFunding)

	mux.HandleFunc("GET /api/admin/pockets/{id}", a.handleAdminDetail)
}
