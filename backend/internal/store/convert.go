package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// ErrInvalidSplit is returned when a brokered conversion names a vendor
// allocation outside (0, item amount): the broker's commission comes out of
// the price the buyer already agreed to, never on top of it.
var ErrInvalidSplit = errors.New("store: vendor allocation must be positive and below the item amount")

// ConvertResult reports a p2p → brokered conversion: the raw link tokens
// minted for the broker's seat and for the vendor's fresh invitation.
type ConvertResult struct {
	PocketID string
	Tokens   map[string]string
}

// ConvertToBrokered turns a buyer-created p2p draft into a three-party
// brokered pocket, on behalf of a middleman who received the vendor
// invitation. The broker takes the broker seat (accepted immediately — naming
// the split is their acceptance), the item amount is split into the vendor's
// allocation plus the broker's commission, and the vendor seat is re-keyed
// with a fresh link token so the invitation the broker received stops being a
// credential. The buyer's total is untouched: allocation + commission equals
// the original amount, which the pockets buyer_total check also enforces.
//
// Only an unclaimed vendor seat on a p2p draft converts; the pocket stays a
// draft afterwards, inert until the real vendor claims and accepts.
func (s *Store) ConvertToBrokered(ctx context.Context, pocketID, brokerUserID string, vendorAmountKobo int64, now time.Time, mint TokenMinter) (ConvertResult, error) {
	res := ConvertResult{PocketID: pocketID, Tokens: make(map[string]string, 2)}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rec, err := s.selectPocketForUpdateTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		if !rec.IsDraft() || rec.Pocket.Structure != pocket.StructureP2P || rec.Pocket.CreatorRole != pocket.RoleBuyer {
			return ErrIllegalState
		}
		if vendorAmountKobo <= 0 || vendorAmountKobo >= rec.Pocket.AmountKobo {
			return ErrInvalidSplit
		}
		vendorSeat, err := getParticipantTx(ctx, tx, pocketID, string(pocket.RoleVendor))
		if err != nil {
			return err
		}
		if vendorSeat.UserID != "" {
			return ErrAlreadyClaimed
		}
		parts, err := participantsTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		for _, p := range parts {
			if p.UserID == brokerUserID {
				return ErrRoleConflict
			}
		}

		commission := rec.Pocket.AmountKobo - vendorAmountKobo
		// The broker becomes the pocket's orchestrator: creator_role moves to
		// broker so the brokered spec (which requires a broker creator) is
		// well-formed when transition #1 constructs the CREATED aggregate.
		tag, err := tx.Exec(ctx, `
			UPDATE pockets SET
				structure = $1,
				creator_role = $2,
				amount_kobo = $3,
				commission_kobo = $4,
				version = version + 1,
				updated_at = now()
			WHERE id = $5 AND version = $6`,
			string(pocket.StructureBrokered), string(pocket.RoleBroker), vendorAmountKobo, commission,
			pocketID, rec.Version,
		)
		if err != nil {
			return fmt.Errorf("convert pocket: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}

		brokerToken, brokerHash, err := mint(pocketID, string(pocket.RoleBroker))
		if err != nil {
			return err
		}
		uid, at := brokerUserID, now
		if err := insertParticipantTx(ctx, tx, pocketID, string(pocket.RoleBroker), &uid, brokerHash, &at); err != nil {
			return err
		}
		res.Tokens[string(pocket.RoleBroker)] = brokerToken

		vendorToken, vendorHash, err := mint(pocketID, string(pocket.RoleVendor))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pocket_participants SET link_token_hash = $1, updated_at = now()
			 WHERE pocket_id = $2 AND role = $3`,
			vendorHash, pocketID, string(pocket.RoleVendor)); err != nil {
			return fmt.Errorf("rekey vendor invitation: %w", err)
		}
		res.Tokens[string(pocket.RoleVendor)] = vendorToken

		payload, _ := json.Marshal(map[string]int64{
			"vendor_amount_kobo": vendorAmountKobo,
			"commission_kobo":    commission,
		})
		return insertEventTx(ctx, tx, pocketID, string(pocket.RoleBroker),
			StateDraft, StateDraft, "converted_to_brokered", payload)
	})
	if err != nil {
		return ConvertResult{}, err
	}
	return res, nil
}
