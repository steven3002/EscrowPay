// Package pocketapp is the application layer: it turns transport-agnostic use
// cases (create, claim, accept, fund, cancel, read) into calls on the store's
// single write path, and then executes the domain effects each transition
// returns. It depends on the store, the payment gateway, and the notifier, but
// knows nothing about HTTP; serialization and authentication live in httpapi.
package pocketapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"escrowpay/internal/gateway"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/notify"
	"escrowpay/internal/pocket"
	"escrowpay/internal/store"
)

// EvidenceStore persists uploaded dispute media and returns an opaque storage
// reference. It is an interface so the application layer does not depend on the
// filesystem implementation (internal/evidence).
type EvidenceStore interface {
	Put(ctx context.Context, pocketID, filename string, r io.Reader, maxBytes int64) (ref string, size int64, err error)
}

// ErrInvalidInput is returned when a request violates a construction invariant.
// Callers map it to a 4xx; it wraps the underlying cause for logging.
var ErrInvalidInput = errors.New("pocketapp: invalid input")

// ErrForbidden is returned when the caller's role may not perform the action.
var ErrForbidden = errors.New("pocketapp: action not permitted for this role")

// App is the application service. Construct it with New.
type App struct {
	store    *store.Store
	gateway  gateway.Gateway
	notifier notify.Notifier
	minter   *linktoken.Minter
	evidence EvidenceStore
	logger   *slog.Logger

	releaseCodeSecret     []byte
	fundingLinkTTL        time.Duration
	gracePeriod           time.Duration
	evidenceCaptureWindow time.Duration
	evidenceMaxBytes      int64
	sandbox               bool

	now func() time.Time
}

// Config carries the dependencies and policy values an App needs.
type Config struct {
	Store    *store.Store
	Gateway  gateway.Gateway
	Notifier notify.Notifier
	Minter   *linktoken.Minter
	Evidence EvidenceStore
	Logger   *slog.Logger

	ReleaseCodeSecret     []byte
	FundingLinkTTL        time.Duration
	GracePeriod           time.Duration
	EvidenceCaptureWindow time.Duration
	EvidenceMaxBytes      int64
	Sandbox               bool

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// New builds an App from cfg.
func New(cfg Config) *App {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		store:                 cfg.Store,
		gateway:               cfg.Gateway,
		notifier:              cfg.Notifier,
		minter:                cfg.Minter,
		evidence:              cfg.Evidence,
		logger:                logger,
		releaseCodeSecret:     cfg.ReleaseCodeSecret,
		fundingLinkTTL:        cfg.FundingLinkTTL,
		gracePeriod:           cfg.GracePeriod,
		evidenceCaptureWindow: cfg.EvidenceCaptureWindow,
		evidenceMaxBytes:      cfg.EvidenceMaxBytes,
		sandbox:               cfg.Sandbox,
		now:                   now,
	}
}

// Sandbox reports whether demo-only affordances (simulate-funding, open admin)
// are enabled.
func (a *App) Sandbox() bool { return a.sandbox }

// EvidenceMaxBytes is the per-upload size cap, exposed so the transport can
// reject an oversize body before buffering it.
func (a *App) EvidenceMaxBytes() int64 { return a.evidenceMaxBytes }

// participantRoles returns the roles a pocket of the given structure carries.
func participantRoles(structure pocket.Structure) []pocket.Role {
	if structure == pocket.StructureBrokered {
		return []pocket.Role{pocket.RoleBuyer, pocket.RoleVendor, pocket.RoleBroker}
	}
	return []pocket.Role{pocket.RoleBuyer, pocket.RoleVendor}
}
