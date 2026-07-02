package pocketapp

import (
	"context"
	"errors"

	"escrowpay/internal/releasecode"
	"escrowpay/internal/store"
)

// ErrCodeNotReady is returned when a Release Code is requested before funding
// has generated one.
var ErrCodeNotReady = errors.New("pocketapp: release code not available yet")

// Load returns a pocket and its participants for a role-scoped view.
func (a *App) Load(ctx context.Context, pocketID string) (store.PocketRecord, []store.ParticipantRecord, error) {
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return store.PocketRecord{}, nil, err
	}
	parts, err := a.store.Participants(ctx, pocketID)
	if err != nil {
		return store.PocketRecord{}, nil, err
	}
	return rec, parts, nil
}

// LoadByShortCode is Load keyed by the shareable short code.
func (a *App) LoadByShortCode(ctx context.Context, shortCode string) (store.PocketRecord, []store.ParticipantRecord, error) {
	rec, err := a.store.GetByShortCode(ctx, shortCode)
	if err != nil {
		return store.PocketRecord{}, nil, err
	}
	parts, err := a.store.Participants(ctx, rec.ID)
	if err != nil {
		return store.PocketRecord{}, nil, err
	}
	return rec, parts, nil
}

// Detail returns a pocket with its participants and full audit timeline, for the
// admin surface.
func (a *App) Detail(ctx context.Context, pocketID string) (store.PocketRecord, []store.ParticipantRecord, []store.EventRecord, error) {
	rec, parts, err := a.Load(ctx, pocketID)
	if err != nil {
		return store.PocketRecord{}, nil, nil, err
	}
	events, err := a.store.Events(ctx, pocketID)
	if err != nil {
		return store.PocketRecord{}, nil, nil, err
	}
	return rec, parts, events, nil
}

// ReleaseCode decrypts and returns the buyer's plaintext Release Code. It is the
// only path by which the plaintext leaves the server; the caller must have
// already authenticated the request as the buyer.
func (a *App) ReleaseCode(ctx context.Context, pocketID string) (string, error) {
	rec, err := a.store.GetByID(ctx, pocketID)
	if err != nil {
		return "", err
	}
	if rec.ReleaseCodeEnc == "" {
		return "", ErrCodeNotReady
	}
	return releasecode.Open(rec.ReleaseCodeEnc, a.releaseCodeSecret)
}
