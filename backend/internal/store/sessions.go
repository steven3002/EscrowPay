package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrSessionInvalid is returned when a session token hash matches no live
// session: unknown, expired, or revoked.
var ErrSessionInvalid = errors.New("store: session invalid")

// InsertSession persists a new session for a user. Only the token's hash is
// stored; the raw token lives exclusively in the caller's cookie.
func (s *Store) InsertSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time, ip, userAgent string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)`,
		userID, tokenHash, expiresAt, ip, userAgent)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// SessionUser resolves a token hash to its user, provided the session is
// neither expired nor revoked, and touches last_seen_at.
func (s *Store) SessionUser(ctx context.Context, tokenHash string, now time.Time) (UserRecord, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		UPDATE sessions SET last_seen_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING user_id`, tokenHash, now).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRecord{}, ErrSessionInvalid
		}
		return UserRecord{}, fmt.Errorf("resolve session: %w", err)
	}
	return s.GetUser(ctx, userID)
}

// RevokeSession invalidates the session with the given token hash. Revoking an
// unknown session is a no-op, so logout is idempotent.
func (s *Store) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
