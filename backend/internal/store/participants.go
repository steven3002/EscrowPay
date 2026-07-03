package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// insertParticipantTx creates one participant row. userID and acceptedAt are nil
// for a counterparty that has not yet claimed or accepted.
func insertParticipantTx(ctx context.Context, tx pgx.Tx, pocketID, role string, userID *string, linkTokenHash string, acceptedAt *time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO pocket_participants (pocket_id, role, user_id, link_token_hash, accepted_at)
		VALUES ($1, $2, $3, $4, $5)`,
		pocketID, role, userID, linkTokenHash, acceptedAt,
	)
	if err != nil {
		return fmt.Errorf("insert participant: %w", err)
	}
	return nil
}

// Participants returns every participant of a pocket, joined with user display
// fields, ordered by role for stable output.
func (s *Store) Participants(ctx context.Context, pocketID string) ([]ParticipantRecord, error) {
	rows, err := s.pool.Query(ctx, participantSelect+` WHERE p.pocket_id = $1 ORDER BY p.role`, pocketID)
	if err != nil {
		return nil, fmt.Errorf("query participants: %w", err)
	}
	defer rows.Close()
	return scanParticipants(rows)
}

const participantSelect = `
	SELECT p.id, p.role, p.user_id, COALESCE(u.display_name, ''), COALESCE(u.phone, ''),
	       p.accepted_at, p.link_token_hash
	FROM pocket_participants p
	LEFT JOIN users u ON u.id = p.user_id`

func scanParticipants(rows pgx.Rows) ([]ParticipantRecord, error) {
	var out []ParticipantRecord
	for rows.Next() {
		rec, err := scanParticipantRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanParticipantRow(r row) (ParticipantRecord, error) {
	var (
		rec        ParticipantRecord
		userID     *string
		acceptedAt *time.Time
	)
	if err := r.Scan(&rec.ID, &rec.Role, &userID, &rec.DisplayName, &rec.Phone, &acceptedAt, &rec.LinkTokenHash); err != nil {
		return ParticipantRecord{}, fmt.Errorf("scan participant: %w", err)
	}
	rec.UserID = derefStr(userID)
	rec.Accepted = acceptedAt != nil
	rec.AcceptedAt = derefTime(acceptedAt)
	return rec, nil
}

// getParticipantTx loads one participant by role within the current transaction.
func getParticipantTx(ctx context.Context, tx pgx.Tx, pocketID, role string) (ParticipantRecord, error) {
	rec, err := scanParticipantRow(tx.QueryRow(ctx, participantSelect+` WHERE p.pocket_id = $1 AND p.role = $2`, pocketID, role))
	if err != nil {
		return ParticipantRecord{}, err
	}
	return rec, nil
}

// participantsTx lists participants within the current transaction (no user
// join needed for acceptance-completeness checks).
func participantsTx(ctx context.Context, tx pgx.Tx, pocketID string) ([]ParticipantRecord, error) {
	rows, err := tx.Query(ctx, `SELECT id, role, user_id, '', '', accepted_at, link_token_hash FROM pocket_participants WHERE pocket_id = $1`, pocketID)
	if err != nil {
		return nil, fmt.Errorf("query participants (tx): %w", err)
	}
	defer rows.Close()
	return scanParticipants(rows)
}

// claimParticipantTx binds userID to a role if it is unclaimed. Re-claiming by
// the same user is idempotent; a different user is rejected, and a user who
// already holds another role in the pocket is rejected (backed by a partial
// unique index on (pocket_id, user_id)).
func claimParticipantTx(ctx context.Context, tx pgx.Tx, pocketID, role, userID string) error {
	existing, err := getParticipantTx(ctx, tx, pocketID, role)
	if err != nil {
		return err
	}
	if existing.UserID != "" {
		if existing.UserID == userID {
			return nil
		}
		return ErrAlreadyClaimed
	}
	parts, err := participantsTx(ctx, tx, pocketID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.UserID == userID {
			return ErrRoleConflict
		}
	}
	_, err = tx.Exec(ctx,
		`UPDATE pocket_participants SET user_id = $1, updated_at = now() WHERE pocket_id = $2 AND role = $3`,
		userID, pocketID, role)
	if err != nil {
		return fmt.Errorf("claim participant: %w", err)
	}
	return nil
}

// markAcceptedTx stamps accepted_at for a role that has not yet accepted,
// returning whether a row was updated.
func markAcceptedTx(ctx context.Context, tx pgx.Tx, pocketID, role string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE pocket_participants SET accepted_at = $1, updated_at = now()
		 WHERE pocket_id = $2 AND role = $3 AND accepted_at IS NULL`,
		now, pocketID, role)
	if err != nil {
		return false, fmt.Errorf("mark accepted: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// roleAccepted reports whether the participant in the given role has accepted.
func roleAccepted(parts []ParticipantRecord, role string) bool {
	for _, p := range parts {
		if p.Role == role {
			return p.Accepted
		}
	}
	return false
}

// allAccepted reports whether every participant has accepted.
func allAccepted(parts []ParticipantRecord) bool {
	for _, p := range parts {
		if !p.Accepted {
			return false
		}
	}
	return len(parts) > 0
}
