package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UserRecord is a users row: the account identity behind sessions and
// participant bindings. Phone and email are optional and unique when present;
// exactly one of them (or a Google subject) anchors the account.
type UserRecord struct {
	ID          string
	Phone       string
	DisplayName string
	Email       string
	AvatarURL   string
	IsAdmin     bool
	TrustTier   int
	Strikes     int
}

const userColumns = `
	id, COALESCE(phone, ''), display_name, COALESCE(email, ''),
	avatar_url, is_admin, trust_tier, strikes`

func scanUser(r row) (UserRecord, error) {
	var u UserRecord
	err := r.Scan(&u.ID, &u.Phone, &u.DisplayName, &u.Email, &u.AvatarURL, &u.IsAdmin, &u.TrustTier, &u.Strikes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRecord{}, ErrNotFound
		}
		return UserRecord{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

// GetUser loads a user by id.
func (s *Store) GetUser(ctx context.Context, id string) (UserRecord, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// UpsertUserByPhone finds a user by phone or creates one, stamping the login
// time. It backs the sandbox demo login.
func (s *Store) UpsertUserByPhone(ctx context.Context, phone, displayName string) (UserRecord, error) {
	var u UserRecord
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		id, err := upsertUserTx(ctx, tx, phone, displayName)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id); err != nil {
			return fmt.Errorf("stamp login: %w", err)
		}
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
		return err
	})
	if err != nil {
		return UserRecord{}, err
	}
	return u, nil
}

// SetAdmin flips a user's admin flag. Admin is the only user-level privilege;
// every other permission is a per-pocket role.
func (s *Store) SetAdmin(ctx context.Context, userID string, isAdmin bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET is_admin = $1, updated_at = now() WHERE id = $2`, isAdmin, userID)
	if err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	return nil
}

// UpsertUserByGoogle finds or creates the account for a verified Google
// identity, keyed by the OIDC subject. A pre-existing account with the same
// verified email (for example one created before the user first signed in with
// Google) is linked by attaching the subject rather than duplicated.
func (s *Store) UpsertUserByGoogle(ctx context.Context, sub, email, displayName, avatarURL string) (UserRecord, error) {
	var u UserRecord
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE google_sub = $1`, sub).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) && email != "" {
			err = tx.QueryRow(ctx, `
				UPDATE users SET google_sub = $1, updated_at = now()
				WHERE email = $2 AND google_sub IS NULL
				RETURNING id`, sub, email).Scan(&id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				INSERT INTO users (google_sub, email, display_name, avatar_url)
				VALUES ($1, NULLIF($2, ''), $3, $4)
				RETURNING id`, sub, email, displayName, avatarURL).Scan(&id)
		}
		if err != nil {
			return fmt.Errorf("upsert google user: %w", err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE users SET
				display_name = CASE WHEN display_name = '' THEN $1 ELSE display_name END,
				avatar_url   = CASE WHEN $2 <> '' THEN $2 ELSE avatar_url END,
				last_login_at = now(),
				updated_at = now()
			WHERE id = $3`, displayName, avatarURL, id)
		if err != nil {
			return fmt.Errorf("refresh google profile: %w", err)
		}
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
		return err
	})
	if err != nil {
		return UserRecord{}, err
	}
	return u, nil
}
