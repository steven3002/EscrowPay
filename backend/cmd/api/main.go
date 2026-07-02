package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"escrowpay/internal/gateway/mock"
	"escrowpay/internal/httpapi"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/notify/logstub"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := loadConfig(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		logger.Error("migration failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("migrations up to date")

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database pool creation failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	// Wire the application: persistence, the mock payment gateway (until s8), the
	// log-stub notifier, and demo-grade link tokens.
	repo := store.New(pool, cfg.FundingLinkTTL, cfg.GracePeriod, cfg.EvidenceCaptureWindow)
	minter := linktoken.NewMinter(cfg.LinkTokenSecret)
	app := pocketapp.New(pocketapp.Config{
		Store:                 repo,
		Gateway:               mock.New(),
		Notifier:              logstub.New(logger),
		Minter:                minter,
		Logger:                logger,
		ReleaseCodeSecret:     cfg.ReleaseCodeSecret,
		FundingLinkTTL:        cfg.FundingLinkTTL,
		GracePeriod:           cfg.GracePeriod,
		EvidenceCaptureWindow: cfg.EvidenceCaptureWindow,
		Sandbox:               cfg.SandboxMode,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(pool))
	httpapi.New(app, minter, logger).Register(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("api listening", slog.String("addr", cfg.ListenAddr), slog.Bool("sandbox", cfg.SandboxMode))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", slog.String("error", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown incomplete", slog.String("error", err.Error()))
	}
	logger.Info("api stopped")
}

func healthzHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		dbStatus := "ok"
		statusCode := http.StatusOK
		if err := pool.Ping(pingCtx); err != nil {
			dbStatus = "unreachable"
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"database": dbStatus,
		})
	}
}
