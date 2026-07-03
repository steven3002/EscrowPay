// Package settlement runs the clock. The Sweeper polls at a fixed interval and,
// each tick, drives every time-triggered transition (funding expiry, delivery
// freeze, grace refund, due settlement) and reconciles any settlement leg left
// pending by a crash. It owns no business logic: each rule is a call into the
// Driver, which the application layer implements over the single write path.
package settlement

import (
	"context"
	"log/slog"
	"time"
)

// Driver is the set of sweep rules the Sweeper drives. Each drains one rule and
// returns how many pockets (or legs) it processed. Implemented by pocketapp.App.
type Driver interface {
	SweepExpiredFunding(ctx context.Context) (int, error)
	SweepDeliveryDeadlines(ctx context.Context) (int, error)
	SweepGraceRefunds(ctx context.Context) (int, error)
	SweepDueSettlements(ctx context.Context) (int, error)
	SweepPendingSettlementLegs(ctx context.Context) (int, error)
}

// Sweeper periodically applies the Driver's rules. Construct it with NewSweeper.
type Sweeper struct {
	driver   Driver
	interval time.Duration
	logger   *slog.Logger
}

// NewSweeper builds a Sweeper. A non-positive interval falls back to one minute.
func NewSweeper(driver Driver, interval time.Duration, logger *slog.Logger) *Sweeper {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Sweeper{driver: driver, interval: interval, logger: logger}
}

// Report tallies one tick's work.
type Report struct {
	Expired        int
	Frozen         int
	GraceRefunded  int
	Settled        int
	LegsReconciled int
}

// Total is the number of state changes and leg disbursements in the report.
func (r Report) Total() int {
	return r.Expired + r.Frozen + r.GraceRefunded + r.Settled + r.LegsReconciled
}

// Run drives a tick immediately and then once per interval until ctx is
// cancelled. It is intended to run in its own goroutine.
func (s *Sweeper) Run(ctx context.Context) {
	s.logger.Info("settlement sweeper started", slog.Duration("interval", s.interval))
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("settlement sweeper stopped")
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// Tick applies every rule once, in dependency order: freezes may create grace
// refunds, and settlements and refunds create legs the reconciliation pass then
// disburses. Rule errors are logged and do not abort the tick; the next tick
// retries. The returned Report is primarily for tests.
func (s *Sweeper) Tick(ctx context.Context) Report {
	var r Report
	r.Expired = s.run(ctx, "expire_funding", s.driver.SweepExpiredFunding)
	r.Frozen = s.run(ctx, "freeze_delivery", s.driver.SweepDeliveryDeadlines)
	r.GraceRefunded = s.run(ctx, "grace_refund", s.driver.SweepGraceRefunds)
	r.Settled = s.run(ctx, "settle_due", s.driver.SweepDueSettlements)
	r.LegsReconciled = s.run(ctx, "reconcile_legs", s.driver.SweepPendingSettlementLegs)
	if n := r.Total(); n > 0 {
		s.logger.Info("sweeper tick",
			slog.Int("expired", r.Expired),
			slog.Int("frozen", r.Frozen),
			slog.Int("grace_refunded", r.GraceRefunded),
			slog.Int("settled", r.Settled),
			slog.Int("legs_reconciled", r.LegsReconciled),
		)
	}
	return r
}

func (s *Sweeper) run(ctx context.Context, rule string, fn func(context.Context) (int, error)) int {
	n, err := fn(ctx)
	if err != nil {
		s.logger.Error("sweeper rule failed", slog.String("rule", rule), slog.String("error", err.Error()))
	}
	return n
}
