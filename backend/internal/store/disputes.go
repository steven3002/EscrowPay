package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// DisputeRecord is a disputes row: the arbitration metadata for a pocket that
// entered DISPUTED. The class fixes the burden of proof; resolution is empty
// until an outcome (concession, force refund, or force payout) closes it.
type DisputeRecord struct {
	PocketID   string
	Class      string
	OpenedBy   string
	State      string // "open" | "resolved"
	Resolution string // "" | "refund" | "payout"
	Notes      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DisputeQueueItem is one row of the admin dispute queue: the dispute joined with
// its pocket's short code and current state.
type DisputeQueueItem struct {
	PocketID  string
	ShortCode string
	State     string
	Class     string
	OpenedBy  string
	CreatedAt time.Time
}

// recordDisputeTx keeps the disputes table in step with the pocket's dispute
// lifecycle inside the transition's own transaction: it opens a dispute row when
// a pocket enters DISPUTED (#10/#12) and resolves that row when the pocket leaves
// DISPUTED for a terminal outcome (#13/#14/#15). The class is read from the
// transition's outcome, not the DB, so it is always the class the domain assigned.
func recordDisputeTx(ctx context.Context, tx pgx.Tx, pocketID, actor string, fromState pocket.State, out pocket.Outcome) error {
	to := out.Pocket.State
	switch {
	case to == pocket.StateDisputed && fromState != pocket.StateDisputed:
		_, err := tx.Exec(ctx, `
			INSERT INTO disputes (pocket_id, class, opened_by)
			VALUES ($1, $2, $3)
			ON CONFLICT (pocket_id) DO NOTHING`,
			pocketID, string(out.Pocket.DisputeClass), actor)
		if err != nil {
			return fmt.Errorf("open dispute: %w", err)
		}
	case fromState == pocket.StateDisputed && (to == pocket.StateRefunded || to == pocket.StateSettled):
		resolution := "refund"
		if to == pocket.StateSettled {
			resolution = "payout"
		}
		_, err := tx.Exec(ctx, `
			UPDATE disputes SET state = 'resolved', resolution = $1, updated_at = now()
			WHERE pocket_id = $2 AND state = 'open'`,
			resolution, pocketID)
		if err != nil {
			return fmt.Errorf("resolve dispute: %w", err)
		}
	}
	return nil
}

// GetDispute returns a pocket's dispute row, or ErrNotFound if none was opened.
func (s *Store) GetDispute(ctx context.Context, pocketID string) (DisputeRecord, error) {
	var d DisputeRecord
	err := s.pool.QueryRow(ctx, `
		SELECT pocket_id, class, opened_by, state, COALESCE(resolution, ''), notes, created_at, updated_at
		FROM disputes WHERE pocket_id = $1`, pocketID).
		Scan(&d.PocketID, &d.Class, &d.OpenedBy, &d.State, &d.Resolution, &d.Notes, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DisputeRecord{}, ErrNotFound
		}
		return DisputeRecord{}, fmt.Errorf("get dispute: %w", err)
	}
	return d, nil
}

// ListOpenDisputes returns the arbitration queue: every unresolved dispute with
// its pocket's short code and state, oldest first.
func (s *Store) ListOpenDisputes(ctx context.Context) ([]DisputeQueueItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.pocket_id, p.short_code, p.state, d.class, d.opened_by, d.created_at
		FROM disputes d
		JOIN pockets p ON p.id = d.pocket_id
		WHERE d.state = 'open'
		ORDER BY d.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list open disputes: %w", err)
	}
	defer rows.Close()

	var out []DisputeQueueItem
	for rows.Next() {
		var it DisputeQueueItem
		if err := rows.Scan(&it.PocketID, &it.ShortCode, &it.State, &it.Class, &it.OpenedBy, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan dispute queue item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RecordSanction records an enforcement action against the pocket participant in
// the given role by incrementing that user's strike count. It is a no-op when the
// role is unclaimed. Sanctions are demo-grade: a fraud flag and a strike are both
// modelled as a strike increment, with the distinguishing kind carried in the
// audit log and the dispute resolution.
func (s *Store) RecordSanction(ctx context.Context, pocketID, role string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET strikes = strikes + 1, updated_at = now()
		WHERE id = (SELECT user_id FROM pocket_participants WHERE pocket_id = $1 AND role = $2)`,
		pocketID, role)
	if err != nil {
		return fmt.Errorf("record sanction: %w", err)
	}
	return nil
}
