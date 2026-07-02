// Package logstub is the MVP Notifier: it writes structured log lines instead
// of sending real messages, so every notification a production channel would
// deliver is observable in development and in tests.
package logstub

import (
	"context"
	"log/slog"

	"escrowpay/internal/notify"
)

type Notifier struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) *Notifier {
	return &Notifier{logger: logger}
}

func (n *Notifier) Send(_ context.Context, e notify.Event) error {
	n.logger.Info("notification",
		slog.String("pocket_id", e.PocketID),
		slog.String("role", e.Role),
		slog.String("kind", e.Kind),
		slog.String("message", e.Message),
	)
	return nil
}
