// Package store owns persistence: schema migrations, pgx-backed repositories,
// and the single transactional write path through which every pocket state
// change flows (see executor.go). The invariant from project-flow §4 —
// SELECT … FOR UPDATE → guard → state write + event insert → commit — lives
// here and nowhere else.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when a lookup matches no row.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict is returned when an optimistic version check fails, meaning a
	// concurrent writer advanced the pocket first.
	ErrConflict = errors.New("store: version conflict")
	// ErrAlreadyAccepted is returned when a participant that has already
	// accepted attempts to accept again.
	ErrAlreadyAccepted = errors.New("store: participant already accepted")
	// ErrNotClaimed is returned when a participant tries to accept before a user
	// has claimed the role.
	ErrNotClaimed = errors.New("store: participant not claimed")
	// ErrAlreadyClaimed is returned when a different user tries to claim an
	// already-claimed role.
	ErrAlreadyClaimed = errors.New("store: role already claimed by another user")
	// ErrIllegalState is returned for an operation the pocket's current state
	// does not permit (distinct from the domain's ErrIllegalTransition, which
	// covers the domain state machine; this covers draft-level operations).
	ErrIllegalState = errors.New("store: operation not allowed in current state")
	// ErrAwaitingVendor is returned when the buyer of a brokered pocket tries to
	// accept before the vendor has accepted. Brokered acceptance is vendor-first:
	// the buyer's link is inert until the vendor confirms the offer.
	ErrAwaitingVendor = errors.New("store: brokered buyer must wait for vendor acceptance")
)

// StateDraft is a persistence-only position that precedes the domain state
// machine. A pocket is created as a draft so it can be shared and claimed; when
// every required participant has accepted, the executor invokes pocket.New to
// enter CREATED (transition #1). The domain machine never sees StateDraft.
const StateDraft = "DRAFT"

// Store is the repository facade over a pgx pool. Policy durations are injected
// so a reloaded pocket is well-formed for the transitions that read them
// (funding TTL at construction; grace and evidence-capture windows at freeze
// and handoff). This matches the blueprint's "config wires into Spec" rule.
type Store struct {
	pool *pgxpool.Pool

	fundingLinkTTL        time.Duration
	gracePeriod           time.Duration
	evidenceCaptureWindow time.Duration
}

// New returns a Store over pool. The durations are platform policy, not
// per-pocket data.
func New(pool *pgxpool.Pool, fundingLinkTTL, gracePeriod, evidenceCaptureWindow time.Duration) *Store {
	return &Store{
		pool:                  pool,
		fundingLinkTTL:        fundingLinkTTL,
		gracePeriod:           gracePeriod,
		evidenceCaptureWindow: evidenceCaptureWindow,
	}
}

// withTx runs fn inside a transaction, committing on success and rolling back on
// error or panic. The deferred rollback after a successful commit is a no-op.
func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}
