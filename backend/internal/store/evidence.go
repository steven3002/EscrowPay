package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EvidenceRecord is one evidence row: a piece of media a participant attached to
// a pocket. WithinWindow is set only for the in-app unboxing video, where it
// records whether the capture fell inside the protection window; it is nil for
// evidence to which the window does not apply.
type EvidenceRecord struct {
	ID            string
	Party         string
	Type          string
	StorageRef    string
	CapturedInApp bool
	CapturedAt    time.Time
	WithinWindow  *bool
	CreatedAt     time.Time
}

// InsertEvidence records a stored piece of evidence and returns its id.
func (s *Store) InsertEvidence(ctx context.Context, pocketID string, ev EvidenceRecord) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO evidence (pocket_id, party, type, storage_ref, captured_in_app, captured_at, within_window)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		pocketID, ev.Party, ev.Type, ev.StorageRef, ev.CapturedInApp, ev.CapturedAt, ev.WithinWindow).
		Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert evidence: %w", err)
	}
	return id, nil
}

// EvidenceForPocket returns every piece of evidence attached to a pocket, oldest
// first.
func (s *Store) EvidenceForPocket(ctx context.Context, pocketID string) ([]EvidenceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, party, type, storage_ref, captured_in_app, captured_at, within_window, created_at
		FROM evidence WHERE pocket_id = $1 ORDER BY created_at, id`, pocketID)
	if err != nil {
		return nil, fmt.Errorf("query evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func scanEvidence(rows pgx.Rows) ([]EvidenceRecord, error) {
	var out []EvidenceRecord
	for rows.Next() {
		var e EvidenceRecord
		if err := rows.Scan(&e.ID, &e.Party, &e.Type, &e.StorageRef,
			&e.CapturedInApp, &e.CapturedAt, &e.WithinWindow, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan evidence: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
