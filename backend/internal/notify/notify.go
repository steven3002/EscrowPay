// Package notify defines the outbound-message boundary. Domain flows emit
// events through this interface without knowing whether they become WhatsApp
// messages, SMS, or log lines.
package notify

import "context"

// Event is one message owed to one participant of one pocket.
type Event struct {
	PocketID string
	Role     string
	Kind     string
	Message  string
}

// Notifier delivers events. Implementations must be safe for concurrent use
// and must not block state transitions on delivery failures.
type Notifier interface {
	Send(ctx context.Context, e Event) error
}
