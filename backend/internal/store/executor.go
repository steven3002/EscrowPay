package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/pocket"
)

// TokenMinter mints a link token for a (pocket, role) pair, returning the raw
// token handed to the participant and the hash the store persists. The store
// takes it as a callback so token cryptography stays out of the persistence
// layer.
type TokenMinter func(pocketID, role string) (token, hash string, err error)

// CreatorInput identifies the participant who authored a pocket. Their
// acceptance is implicit at creation.
type CreatorInput struct {
	Role        pocket.Role
	Phone       string
	DisplayName string
}

// CreateResult reports a newly created draft: its identity and the raw link
// token minted for each role (the counterparty's is the one to share).
type CreateResult struct {
	PocketID  string
	ShortCode string
	Tokens    map[string]string
}

// AcceptResult reports the outcome of an acceptance. Completed is true only for
// the acceptance that satisfied every participant and fired transition #1; in
// that case Outcome carries the CREATED pocket and its effects.
type AcceptResult struct {
	PocketID  string
	Completed bool
	State     pocket.State
	Outcome   pocket.Outcome
}

// WriteResult reports a state transition run through the executor.
type WriteResult struct {
	PocketID  string
	FromState pocket.State
	Outcome   pocket.Outcome
}

// CreatePocket creates a draft pocket with its participant rows in one
// transaction. The creator's role is bound and pre-accepted; every role in
// roles receives a freshly minted link token.
func (s *Store) CreatePocket(ctx context.Context, draft PocketDraft, creator CreatorInput, roles []pocket.Role, now time.Time, mint TokenMinter) (CreateResult, error) {
	res := CreateResult{Tokens: make(map[string]string, len(roles))}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		id, shortCode, err := s.insertPocketDraftTx(ctx, tx, draft)
		if err != nil {
			return err
		}
		creatorUserID, err := upsertUserTx(ctx, tx, creator.Phone, creator.DisplayName)
		if err != nil {
			return err
		}
		for _, role := range roles {
			token, hash, err := mint(id, string(role))
			if err != nil {
				return err
			}
			var userID *string
			var acceptedAt *time.Time
			if role == creator.Role {
				uid := creatorUserID
				at := now
				userID, acceptedAt = &uid, &at
			}
			if err := insertParticipantTx(ctx, tx, id, string(role), userID, hash, acceptedAt); err != nil {
				return err
			}
			res.Tokens[string(role)] = token
		}
		res.PocketID, res.ShortCode = id, shortCode
		return nil
	})
	if err != nil {
		return CreateResult{}, err
	}
	return res, nil
}

// Claim binds a user (found or created by phone) to a role, provided the pocket
// is still a draft and the role is unclaimed. Re-claiming by the same user is a
// no-op; a different user is rejected.
func (s *Store) Claim(ctx context.Context, pocketID, role, phone, displayName string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		rec, err := s.selectPocketForUpdateTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		if !rec.IsDraft() {
			return ErrIllegalState
		}
		userID, err := upsertUserTx(ctx, tx, phone, displayName)
		if err != nil {
			return err
		}
		return claimParticipantTx(ctx, tx, pocketID, role, userID)
	})
}

// Accept records a role's acceptance and, when it is the last one outstanding,
// fires transition #1 (draft → CREATED) in the same transaction. A buyer may
// supply the delivery address at this point.
func (s *Store) Accept(ctx context.Context, pocketID, role, deliveryAddress, actor string, now time.Time) (AcceptResult, error) {
	var result AcceptResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rec, err := s.selectPocketForUpdateTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		if !rec.IsDraft() {
			return ErrIllegalState
		}
		part, err := getParticipantTx(ctx, tx, pocketID, role)
		if err != nil {
			return err
		}
		if part.UserID == "" {
			return ErrNotClaimed
		}
		if part.Accepted {
			return ErrAlreadyAccepted
		}
		// Brokered acceptance is vendor-first: the buyer's link is inert until
		// the vendor has accepted the offer. Enforced here in the write path,
		// never in the UI.
		if rec.Pocket.Structure == pocket.StructureBrokered && role == string(pocket.RoleBuyer) {
			parts, err := participantsTx(ctx, tx, pocketID)
			if err != nil {
				return err
			}
			if !roleAccepted(parts, string(pocket.RoleVendor)) {
				return ErrAwaitingVendor
			}
		}
		accepted, err := markAcceptedTx(ctx, tx, pocketID, role, now)
		if err != nil {
			return err
		}
		if !accepted {
			return ErrAlreadyAccepted
		}
		if role == string(pocket.RoleBuyer) && deliveryAddress != "" {
			if err := setDeliveryAddressTx(ctx, tx, pocketID, deliveryAddress); err != nil {
				return err
			}
		}
		parts, err := participantsTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		if !allAccepted(parts) {
			result = AcceptResult{PocketID: pocketID, Completed: false, State: pocket.State(StateDraft)}
			return nil
		}

		out, err := pocket.New(now, s.specFromRecord(rec))
		if err != nil {
			return err
		}
		if err := updatePocketTx(ctx, tx, pocketID, out.Pocket, rec.Version); err != nil {
			return err
		}
		if err := insertEventTx(ctx, tx, pocketID, actor, StateDraft, string(out.Pocket.State), "created", nil); err != nil {
			return err
		}
		result = AcceptResult{PocketID: pocketID, Completed: true, State: out.Pocket.State, Outcome: out}
		return nil
	})
	if err != nil {
		return AcceptResult{}, err
	}
	return result, nil
}

// RunTransition applies a domain event to a live pocket through the single write
// path: row-lock, guard, state write + event, commit. Effects in the returned
// Outcome are executed by the caller after commit.
func (s *Store) RunTransition(ctx context.Context, pocketID, actor string, ev pocket.Event, now time.Time) (WriteResult, error) {
	var result WriteResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rec, err := s.selectPocketForUpdateTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		if rec.IsDraft() {
			return ErrIllegalState
		}
		result, err = applyTransitionTx(ctx, tx, pocketID, actor, rec, now, ev)
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	return result, nil
}

// Cancel terminates a pocket according to its current position: a never-accepted
// draft is cancelled directly; a CREATED pocket takes transition #4; a FUNDED
// pocket takes transition #5 (refund). Any later state is rejected.
func (s *Store) Cancel(ctx context.Context, pocketID, actor string, now time.Time) (WriteResult, error) {
	var result WriteResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rec, err := s.selectPocketForUpdateTx(ctx, tx, pocketID)
		if err != nil {
			return err
		}
		switch {
		case rec.IsDraft():
			next := rec.Pocket
			next.State = pocket.StateCancelled
			if err := updatePocketTx(ctx, tx, pocketID, next, rec.Version); err != nil {
				return err
			}
			if err := insertEventTx(ctx, tx, pocketID, actor, StateDraft, string(pocket.StateCancelled), "cancel", nil); err != nil {
				return err
			}
			result = WriteResult{PocketID: pocketID, FromState: pocket.State(StateDraft), Outcome: pocket.Outcome{Pocket: next}}
			return nil
		case rec.Pocket.State == pocket.StateCreated:
			result, err = applyTransitionTx(ctx, tx, pocketID, actor, rec, now, pocket.Event{Kind: pocket.EvCancel})
			return err
		case rec.Pocket.State == pocket.StateFunded:
			result, err = applyTransitionTx(ctx, tx, pocketID, actor, rec, now, pocket.Event{Kind: pocket.EvVendorCancel})
			return err
		default:
			return ErrIllegalState
		}
	})
	if err != nil {
		return WriteResult{}, err
	}
	return result, nil
}

// applyTransitionTx is the shared body of every domain transition: it runs the
// pure guard, persists the resulting snapshot with an optimistic version check,
// and appends exactly one audit row — all inside the caller's transaction.
func applyTransitionTx(ctx context.Context, tx pgx.Tx, pocketID, actor string, rec PocketRecord, now time.Time, ev pocket.Event) (WriteResult, error) {
	out, err := rec.Pocket.Transition(now, ev)
	if err != nil {
		return WriteResult{}, err
	}
	if err := updatePocketTx(ctx, tx, pocketID, out.Pocket, rec.Version); err != nil {
		return WriteResult{}, err
	}
	if err := insertEventTx(ctx, tx, pocketID, actor, string(rec.Pocket.State), string(out.Pocket.State), string(ev.Kind), eventPayload(ev)); err != nil {
		return WriteResult{}, err
	}
	if err := recordSettlementLegsTx(ctx, tx, pocketID, out); err != nil {
		return WriteResult{}, err
	}
	if err := recordDisputeTx(ctx, tx, pocketID, actor, rec.Pocket.State, out); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{PocketID: pocketID, FromState: rec.Pocket.State, Outcome: out}, nil
}

func eventPayload(ev pocket.Event) []byte {
	if ev.Reason == "" && !ev.BadFaith {
		return nil
	}
	m := map[string]any{}
	if ev.Reason != "" {
		m["reason"] = ev.Reason
	}
	if ev.BadFaith {
		m["bad_faith"] = true
	}
	b, _ := json.Marshal(m)
	return b
}
