package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// config holds the process configuration resolved from the environment. Secrets
// fall back to fixed development values so the demo runs out of the box; a
// production deployment must set them explicitly (see loadConfig warnings).
type config struct {
	DatabaseURL string
	ListenAddr  string

	LinkTokenSecret   []byte
	ReleaseCodeSecret []byte

	// SandboxMode gates demo-only affordances such as simulate-funding and the
	// open admin surface. Production payment integration flips the default to
	// false.
	SandboxMode bool

	// Pocket policy durations applied when a pocket is constructed and reloaded.
	FundingLinkTTL        time.Duration
	GracePeriod           time.Duration
	EvidenceCaptureWindow time.Duration

	// SweeperEnabled turns the in-process settlement sweeper on; SweeperInterval
	// is the poll period between reconciliation passes.
	SweeperEnabled  bool
	SweeperInterval time.Duration

	// EvidenceDir is the local directory dispute media is written to;
	// EvidenceMaxBytes caps a single upload.
	EvidenceDir      string
	EvidenceMaxBytes int64

	// SessionTTL bounds how long a sign-in lasts; CookieSecure must be true
	// wherever the app is served over HTTPS.
	SessionTTL   time.Duration
	CookieSecure bool

	// TrustProxy keys rate limits on X-Forwarded-For; enable only when every
	// request arrives through the app's own reverse proxy.
	TrustProxy bool
	// TrustedOrigins are extra browser origins allowed to make mutating
	// requests (e.g. a frontend hosted on a different origin that proxies here).
	TrustedOrigins []string
	// RateLimitEnabled turns the per-client request limiters on.
	RateLimitEnabled bool

	// Google OIDC sign-in. Enabled only when client id, secret and redirect
	// URL are all present; the issuer is discoverable and overridable for
	// tests.
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	GoogleIssuer       string

	// GatewayProvider selects the payment rails: "mock" (default, no money
	// moves) or "nomba" (real provider; sandbox or production per base URL).
	GatewayProvider string
	// SimulateFundingEnabled keeps the sandbox funding shortcut available.
	// It defaults to true on the mock gateway and false on a real one, where
	// it may be re-enabled explicitly as a demo fallback.
	SimulateFundingEnabled bool
	Nomba                  nombaConfig
}

// nombaConfig carries the Nomba credentials and policy. The parent account id
// authenticates every call via the accountId header; money operations are
// scoped to the sub-account.
type nombaConfig struct {
	BaseURL               string
	ClientID              string
	ClientSecret          string
	ParentAccountID       string
	SubAccountID          string
	SignatureKey          string
	PublicBaseURL         string
	FallbackCustomerEmail string
	PayoutAccountNumber   string
	PayoutBankCode        string
	PayoutAccountName     string
	RefundAccountNumber   string
	RefundBankCode        string
	RefundAccountName     string
}

// devLinkTokenSecret and devReleaseCodeSecret are used only when the
// corresponding environment variables are unset. They are not secret and must
// never protect real funds.
const (
	devLinkTokenSecret   = "escrowpay-dev-link-token-secret-change-me"
	devReleaseCodeSecret = "escrowpay-dev-release-code-secret-change-me"
)

func loadConfig(logger *slog.Logger) config {
	cfg := config{
		DatabaseURL:           envOr("DATABASE_URL", "postgres://escrowpay:escrowpay_dev@localhost:5433/escrowpay"),
		ListenAddr:            envOr("LISTEN_ADDR", ":8080"),
		LinkTokenSecret:       []byte(envOr("LINK_TOKEN_SECRET", "")),
		ReleaseCodeSecret:     []byte(envOr("RELEASE_CODE_SECRET", "")),
		SandboxMode:           envBool("SANDBOX_MODE", true),
		FundingLinkTTL:        envHours("FUNDING_LINK_TTL_HOURS", 72),
		GracePeriod:           envHours("GRACE_HOURS", 24),
		EvidenceCaptureWindow: envMinutes("EVIDENCE_CAPTURE_WINDOW_MINUTES", 60),
		SweeperEnabled:        envBool("SWEEPER_ENABLED", true),
		SweeperInterval:       envSeconds("SWEEPER_INTERVAL_SECONDS", 60),
		EvidenceDir:           envOr("EVIDENCE_DIR", "./data/evidence"),
		EvidenceMaxBytes:      int64(envInt("EVIDENCE_MAX_MB", 25)) << 20,
		SessionTTL:            envHours("SESSION_TTL_HOURS", 720),
		CookieSecure:          envBool("COOKIE_SECURE", false),
		TrustProxy:            envBool("TRUST_PROXY", true),
		TrustedOrigins:        envList("TRUSTED_ORIGINS"),
		RateLimitEnabled:      envBool("RATE_LIMIT_ENABLED", true),
		GoogleClientID:        envOr("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:    envOr("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:     envOr("GOOGLE_REDIRECT_URL", ""),
		GoogleIssuer:          envOr("GOOGLE_ISSUER", ""),
		GatewayProvider:       envOr("GATEWAY_PROVIDER", "mock"),
		Nomba: nombaConfig{
			BaseURL: envOr("NOMBA_BASE_URL", "https://sandbox.nomba.com"),
			// The credential variables accept the names the Nomba dashboard's
			// test-credentials export uses, so a pasted .env works unchanged.
			ClientID:              envOr("NOMBA_CLIENT_ID", envOr("TEST_CREDENTIALS_CLIENT_ID", "")),
			ClientSecret:          envOr("NOMBA_CLIENT_SECRET", envOr("TEST_CREDENTIALS_CLIENT_SECRET", "")),
			ParentAccountID:       envOr("NOMBA_PARENT_ACCOUNT_ID", envOr("PARENT_ACCOUNT_ID", "")),
			SubAccountID:          envOr("NOMBA_SUB_ACCOUNT_ID", envOr("SUB_ACCOUNT_ID", "")),
			SignatureKey:          envOr("NOMBA_SIGNATURE_KEY", ""),
			PublicBaseURL:         envOr("PUBLIC_BASE_URL", "http://localhost:3000"),
			FallbackCustomerEmail: envOr("NOMBA_FALLBACK_CUSTOMER_EMAIL", ""),
			PayoutAccountNumber:   envOr("NOMBA_PAYOUT_ACCOUNT_NUMBER", ""),
			PayoutBankCode:        envOr("NOMBA_PAYOUT_BANK_CODE", ""),
			PayoutAccountName:     envOr("NOMBA_PAYOUT_ACCOUNT_NAME", ""),
			RefundAccountNumber:   envOr("NOMBA_REFUND_ACCOUNT_NUMBER", ""),
			RefundBankCode:        envOr("NOMBA_REFUND_BANK_CODE", ""),
			RefundAccountName:     envOr("NOMBA_REFUND_ACCOUNT_NAME", ""),
		},
	}
	cfg.SimulateFundingEnabled = envBool("SIMULATE_FUNDING_ENABLED", cfg.GatewayProvider == "mock")

	// Serverless container platforms (Cloud Run and similar) inject the port to
	// listen on via PORT; honor it over LISTEN_ADDR when present.
	if port := os.Getenv("PORT"); port != "" {
		cfg.ListenAddr = ":" + port
	}

	if len(cfg.LinkTokenSecret) == 0 {
		logger.Warn("LINK_TOKEN_SECRET unset; using an insecure development secret")
		cfg.LinkTokenSecret = []byte(devLinkTokenSecret)
	}
	if len(cfg.ReleaseCodeSecret) == 0 {
		logger.Warn("RELEASE_CODE_SECRET unset; using an insecure development secret")
		cfg.ReleaseCodeSecret = []byte(devReleaseCodeSecret)
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envList parses a comma-separated environment variable into a trimmed,
// non-empty slice. An unset or empty value yields nil.
func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func envHours(key string, fallbackHours int) time.Duration {
	return time.Duration(envInt(key, fallbackHours)) * time.Hour
}

func envMinutes(key string, fallbackMinutes int) time.Duration {
	return time.Duration(envInt(key, fallbackMinutes)) * time.Minute
}

func envSeconds(key string, fallbackSeconds int) time.Duration {
	return time.Duration(envInt(key, fallbackSeconds)) * time.Second
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}
