package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// insertEventTx appends one audit row. It is called exactly once per state
// change, inside the same transaction as the state write, so the event log is a
// faithful, gap-free history.
func insertEventTx(ctx context.Context, tx pgx.Tx, pocketID, actor, fromState, toState, kind string, payload []byte) error {
	if payload == nil {
		payload = []byte("{}")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO pocket_events (pocket_id, actor, from_state, to_state, kind, payload)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		pocketID, actor, nullStr(fromState), nullStr(toState), kind, payload,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// Events returns a pocket's audit timeline in chronological order.
func (s *Store) Events(ctx context.Context, pocketID string) ([]EventRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor, COALESCE(from_state, ''), COALESCE(to_state, ''), kind, created_at
		FROM pocket_events WHERE pocket_id = $1 ORDER BY id`, pocketID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.ID, &e.Actor, &e.FromState, &e.ToState, &e.Kind, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEvents returns the number of audit rows for a pocket. Tests assert this
// equals the number of state changes.
func (s *Store) CountEvents(ctx context.Context, pocketID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pocket_events WHERE pocket_id = $1`, pocketID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return n, nil
}
