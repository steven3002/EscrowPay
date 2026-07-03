package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// pocketColumns is the canonical SELECT list, ordered to match scanPocket.
const pocketColumns = `
	id, short_code, version, structure, creator_role, mode,
	amount_kobo, commission_kobo, premium_kobo,
	item_description, category, delivery_address,
	inspection_window_minutes, delivery_window_minutes,
	state, release_code_hash, code_attempts, code_locked,
	delivery_deadline, settle_after, grace_deadline, funding_expires_at,
	funding_link_ref, funding_link_url, release_code_enc, created_at`

// row is the read interface shared by pool.QueryRow and tx.QueryRow.
type row interface {
	Scan(dest ...any) error
}

// scanPocket reads one pocket row into a PocketRecord. Any extra destinations
// are appended to the scan, so a caller joining additional columns after the
// canonical list can reuse the same reader.
func (s *Store) scanPocket(r row, extra ...any) (PocketRecord, error) {
	var (
		rec                                                              PocketRecord
		structure, creatorRole, mode, state                              string
		inspectionMin, deliveryMin                                       int
		deliveryAddress, releaseHash, fundingRef, fundingURL, releaseEnc *string
		deliveryDeadline, settleAfter, graceDeadline, fundingExpires     *time.Time
	)
	dest := []any{
		&rec.ID, &rec.ShortCode, &rec.Version, &structure, &creatorRole, &mode,
		&rec.Pocket.AmountKobo, &rec.Pocket.CommissionKobo, &rec.Pocket.PremiumKobo,
		&rec.ItemDescription, &rec.Category, &deliveryAddress,
		&inspectionMin, &deliveryMin,
		&state, &releaseHash, &rec.Pocket.CodeAttempts, &rec.Pocket.CodeLocked,
		&deliveryDeadline, &settleAfter, &graceDeadline, &fundingExpires,
		&fundingRef, &fundingURL, &releaseEnc, &rec.CreatedAt,
	}
	err := r.Scan(append(dest, extra...)...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PocketRecord{}, ErrNotFound
		}
		return PocketRecord{}, fmt.Errorf("scan pocket: %w", err)
	}

	rec.Pocket.State = pocket.State(state)
	rec.Pocket.Structure = pocket.Structure(structure)
	rec.Pocket.CreatorRole = pocket.Role(creatorRole)
	rec.Pocket.Mode = pocket.Mode(mode)
	rec.Pocket.InspectionWindow = time.Duration(inspectionMin) * time.Minute
	rec.Pocket.DeliveryWindow = time.Duration(deliveryMin) * time.Minute
	rec.Pocket.DeliveryDeadline = derefTime(deliveryDeadline)
	rec.Pocket.SettleAfter = derefTime(settleAfter)
	rec.Pocket.GraceDeadline = derefTime(graceDeadline)
	rec.Pocket.FundingExpiresAt = derefTime(fundingExpires)
	// Policy durations are not stored per-pocket; inject them so the aggregate
	// is well-formed for transitions that read them.
	rec.Pocket.GracePeriod = s.gracePeriod
	rec.Pocket.EvidenceCaptureWindow = s.evidenceCaptureWindow

	rec.DeliveryAddress = derefStr(deliveryAddress)
	rec.ReleaseCodeHash = derefStr(releaseHash)
	rec.FundingLinkRef = derefStr(fundingRef)
	rec.FundingLinkURL = derefStr(fundingURL)
	rec.ReleaseCodeEnc = derefStr(releaseEnc)
	return rec, nil
}

// GetByID loads a pocket by its UUID.
func (s *Store) GetByID(ctx context.Context, id string) (PocketRecord, error) {
	return s.scanPocket(s.pool.QueryRow(ctx, `SELECT `+pocketColumns+` FROM pockets WHERE id = $1`, id))
}

// GetByShortCode loads a pocket by its shareable short code.
func (s *Store) GetByShortCode(ctx context.Context, shortCode string) (PocketRecord, error) {
	return s.scanPocket(s.pool.QueryRow(ctx, `SELECT `+pocketColumns+` FROM pockets WHERE short_code = $1`, shortCode))
}

// insertPocketDraftTx inserts a DRAFT pocket and returns its generated id and
// short code. Participants are inserted separately within the same transaction.
func (s *Store) insertPocketDraftTx(ctx context.Context, tx pgx.Tx, d PocketDraft) (id, shortCode string, err error) {
	shortCode, err = newShortCode()
	if err != nil {
		return "", "", err
	}
	buyerTotal := d.AmountKobo + d.CommissionKobo + d.PremiumKobo
	err = tx.QueryRow(ctx, `
		INSERT INTO pockets (
			short_code, structure, creator_role, mode,
			amount_kobo, commission_kobo, premium_kobo, buyer_total_kobo,
			item_description, category,
			inspection_window_minutes, delivery_window_minutes, state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id`,
		shortCode, string(d.Structure), string(d.CreatorRole), string(d.Mode),
		d.AmountKobo, d.CommissionKobo, d.PremiumKobo, buyerTotal,
		d.ItemDescription, d.Category,
		minutes(d.InspectionWindow), minutes(d.DeliveryWindow), StateDraft,
	).Scan(&id)
	if err != nil {
		return "", "", fmt.Errorf("insert pocket draft: %w", err)
	}
	return id, shortCode, nil
}

// selectPocketForUpdateTx loads and row-locks a pocket for the duration of the
// transaction. It is the head of the single write path.
func (s *Store) selectPocketForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (PocketRecord, error) {
	return s.scanPocket(tx.QueryRow(ctx, `SELECT `+pocketColumns+` FROM pockets WHERE id = $1 FOR UPDATE`, id))
}

// updatePocketTx persists the domain-owned mutable fields of p and bumps the
// version, guarding on the expected version. A zero RowsAffected means a
// concurrent writer won the race.
func updatePocketTx(ctx context.Context, tx pgx.Tx, id string, p pocket.Pocket, expectedVersion int) error {
	tag, err := tx.Exec(ctx, `
		UPDATE pockets SET
			state = $1,
			code_attempts = $2,
			code_locked = $3,
			delivery_deadline = $4,
			settle_after = $5,
			grace_deadline = $6,
			funding_expires_at = $7,
			version = version + 1,
			updated_at = now()
		WHERE id = $8 AND version = $9`,
		string(p.State), p.CodeAttempts, p.CodeLocked,
		nullTime(p.DeliveryDeadline), nullTime(p.SettleAfter),
		nullTime(p.GraceDeadline), nullTime(p.FundingExpiresAt),
		id, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update pocket: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func setDeliveryAddressTx(ctx context.Context, tx pgx.Tx, id, address string) error {
	_, err := tx.Exec(ctx, `UPDATE pockets SET delivery_address = $1, updated_at = now() WHERE id = $2`, address, id)
	if err != nil {
		return fmt.Errorf("set delivery address: %w", err)
	}
	return nil
}

// SetFundingLink records the gateway funding artifact minted at acceptance. It
// runs outside the transition transaction as an effect and is idempotent: the
// mock mints a deterministic reference, so a retry stores the same values.
func (s *Store) SetFundingLink(ctx context.Context, id, ref, url string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE pockets SET funding_link_ref = $1, funding_link_url = $2, updated_at = now() WHERE id = $3`,
		ref, url, id)
	if err != nil {
		return fmt.Errorf("set funding link: %w", err)
	}
	return nil
}

// SetReleaseCode records the code's HMAC verifier and its encrypted,
// buyer-retrievable copy. It only writes when no code is stored yet, so a retry
// of the funding effect never rotates a code the buyer may already hold.
func (s *Store) SetReleaseCode(ctx context.Context, id, hash, enc string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE pockets SET release_code_hash = $1, release_code_enc = $2, updated_at = now()
		 WHERE id = $3 AND release_code_hash IS NULL`,
		hash, enc, id)
	if err != nil {
		return fmt.Errorf("set release code: %w", err)
	}
	return nil
}

// UserPocket is one row of a user's cross-pocket dashboard: the pocket record
// plus the role that user holds in it.
type UserPocket struct {
	Record PocketRecord
	Role   string
}

// PocketsForUser returns every pocket the user participates in, newest first,
// with the user's role in each. It feeds the dashboard; role-scoped
// serialization of each record remains the transport layer's responsibility.
func (s *Store) PocketsForUser(ctx context.Context, userID string, limit int) ([]UserPocket, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixColumns(pocketColumns, "p.")+`, pp.role
		FROM pocket_participants pp
		JOIN pockets p ON p.id = pp.pocket_id
		WHERE pp.user_id = $1
		ORDER BY p.created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query user pockets: %w", err)
	}
	defer rows.Close()

	var out []UserPocket
	for rows.Next() {
		var role string
		rec, err := s.scanPocket(rows, &role)
		if err != nil {
			return nil, err
		}
		out = append(out, UserPocket{Record: rec, Role: role})
	}
	return out, rows.Err()
}

// prefixColumns qualifies each column in a comma-separated SELECT list with a
// table alias, so the canonical pocket column list can be reused in joins.
func prefixColumns(columns, prefix string) string {
	parts := strings.Split(columns, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// upsertUserTx finds a user by phone or creates one, returning the user id.
func upsertUserTx(ctx context.Context, tx pgx.Tx, phone, displayName string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO users (phone, display_name) VALUES ($1, $2)
		ON CONFLICT (phone) DO UPDATE SET display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), users.display_name)
		RETURNING id`,
		phone, displayName,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert user: %w", err)
	}
	return id, nil
}

func minutes(d time.Duration) int {
	return int(d / time.Minute)
}

// newShortCode returns a lowercase, unpadded base32 short code with ~48 bits of
// entropy — collision-negligible, and the unique constraint is the backstop.
func newShortCode() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate short code: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}
