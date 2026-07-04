package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"escrowpay/internal/auth"
	"escrowpay/internal/evidence"
	"escrowpay/internal/gateway"
	"escrowpay/internal/gateway/mock"
	"escrowpay/internal/gateway/nomba"
	"escrowpay/internal/httpapi"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/notify/logstub"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/settlement"
	"escrowpay/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if applied := loadDotEnv(".env"); applied > 0 {
		logger.Info("environment loaded from .env", slog.Int("values", applied))
	}
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

	// Wire the application: persistence, the configured payment gateway, the
	// log-stub notifier, and demo-grade link tokens.
	repo := store.New(pool, cfg.FundingLinkTTL, cfg.GracePeriod, cfg.EvidenceCaptureWindow)
	minter := linktoken.NewMinter(cfg.LinkTokenSecret)
	evidenceStore, err := evidence.NewFileStore(cfg.EvidenceDir)
	if err != nil {
		logger.Error("evidence store init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	gw, webhookVerifier, err := buildGateway(cfg, logger)
	if err != nil {
		logger.Error("gateway configuration invalid", slog.String("error", err.Error()))
		os.Exit(1)
	}
	realGateway := cfg.GatewayProvider != "mock"

	app := pocketapp.New(pocketapp.Config{
		Store:                  repo,
		Gateway:                gw,
		Notifier:               logstub.New(logger),
		Minter:                 minter,
		Evidence:               evidenceStore,
		Logger:                 logger,
		ReleaseCodeSecret:      cfg.ReleaseCodeSecret,
		FundingLinkTTL:         cfg.FundingLinkTTL,
		GracePeriod:            cfg.GracePeriod,
		EvidenceCaptureWindow:  cfg.EvidenceCaptureWindow,
		EvidenceMaxBytes:       cfg.EvidenceMaxBytes,
		Sandbox:                cfg.SandboxMode,
		RealGateway:            realGateway,
		DisableSimulateFunding: !cfg.SimulateFundingEnabled,
	})

	// The settlement sweeper drives every clock-triggered transition and
	// reconciles unpaid legs. It shares the process lifecycle: ctx cancellation
	// on shutdown stops its loop.
	if cfg.SweeperEnabled {
		go settlement.NewSweeper(app, cfg.SweeperInterval, logger).Run(ctx)
	} else {
		logger.Warn("settlement sweeper disabled; timer-driven transitions will not fire")
	}

	sessions := auth.NewManager(repo, cfg.SessionTTL, cfg.CookieSecure, nil)
	var google *auth.Google
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" && cfg.GoogleRedirectURL != "" {
		google = auth.NewGoogle(auth.GoogleConfig{
			Issuer:       cfg.GoogleIssuer,
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL,
		})
		logger.Info("google sign-in enabled")
	} else {
		logger.Warn("google sign-in not configured; only sandbox demo login is available")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(pool))
	api := httpapi.New(httpapi.Config{
		App:          app,
		Minter:       minter,
		Auth:         sessions,
		Users:        repo,
		Google:       google,
		NombaWebhook: webhookVerifier,
		Logger:       logger,
		FlowSecret:     cfg.LinkTokenSecret,
		CookieSecure:   cfg.CookieSecure,
		TrustProxy:     cfg.TrustProxy,
		TrustedOrigins: cfg.TrustedOrigins,
		RateLimit:      cfg.RateLimitEnabled,
	})
	api.Register(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Middleware(mux),
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

// buildGateway constructs the configured payment gateway and, for a real
// provider, the webhook verifier that authenticates its notifications. An
// unusable real-gateway configuration is fatal: silently falling back to the
// mock would let a deployment believe money moves when it does not.
func buildGateway(cfg config, logger *slog.Logger) (gateway.Gateway, *nomba.WebhookVerifier, error) {
	switch cfg.GatewayProvider {
	case "", "mock":
		logger.Info("payment gateway: mock (no money moves)")
		return mock.New(), nil, nil
	case "nomba":
		client, err := nomba.New(nomba.Config{
			BaseURL:               cfg.Nomba.BaseURL,
			ClientID:              cfg.Nomba.ClientID,
			ClientSecret:          cfg.Nomba.ClientSecret,
			AccountID:             cfg.Nomba.ParentAccountID,
			SubAccountID:          cfg.Nomba.SubAccountID,
			PublicBaseURL:         cfg.Nomba.PublicBaseURL,
			FallbackCustomerEmail: cfg.Nomba.FallbackCustomerEmail,
			PayoutBeneficiary: nomba.Beneficiary{
				AccountNumber: cfg.Nomba.PayoutAccountNumber,
				BankCode:      cfg.Nomba.PayoutBankCode,
				AccountName:   cfg.Nomba.PayoutAccountName,
			},
			RefundBeneficiary: nomba.Beneficiary{
				AccountNumber: cfg.Nomba.RefundAccountNumber,
				BankCode:      cfg.Nomba.RefundBankCode,
				AccountName:   cfg.Nomba.RefundAccountName,
			},
			Logger: logger,
		})
		if err != nil {
			return nil, nil, err
		}
		var verifier *nomba.WebhookVerifier
		if cfg.Nomba.SignatureKey != "" {
			verifier = nomba.NewWebhookVerifier([]byte(cfg.Nomba.SignatureKey))
		} else {
			logger.Warn("NOMBA_SIGNATURE_KEY unset; webhook ingestion disabled — funding will not confirm without it")
		}
		logger.Info("payment gateway: nomba", slog.String("base_url", cfg.Nomba.BaseURL))
		return client, verifier, nil
	default:
		return nil, nil, fmt.Errorf("unknown GATEWAY_PROVIDER %q (want mock or nomba)", cfg.GatewayProvider)
	}
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
