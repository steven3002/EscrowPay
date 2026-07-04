package store

import (
	"context"
	"fmt"
)

// RecordWebhookEvent stores a provider notification exactly once, keyed on the
// provider's event id, and reports whether the event still needs processing.
// A replay of an already-processed event returns false; a redelivery of an
// event whose processing did not complete returns true, so the (idempotent)
// processing gets another chance. Two concurrent deliveries may both see true;
// downstream processing tolerates that by construction.
func (s *Store) RecordWebhookEvent(ctx context.Context, providerEventID string, payload []byte) (needsProcessing bool, err error) {
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO webhook_events (provider_event_id, payload)
		VALUES ($1, $2)
		ON CONFLICT (provider_event_id) DO UPDATE SET provider_event_id = EXCLUDED.provider_event_id
		RETURNING processed_at IS NULL`,
		providerEventID, payload,
	).Scan(&needsProcessing)
	if err != nil {
		return false, fmt.Errorf("record webhook event: %w", err)
	}
	return needsProcessing, nil
}

// MarkWebhookProcessed stamps an event as fully handled, ending redelivery
// processing for it.
func (s *Store) MarkWebhookProcessed(ctx context.Context, providerEventID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_events SET processed_at = now() WHERE provider_event_id = $1 AND processed_at IS NULL`,
		providerEventID)
	if err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	return nil
}
