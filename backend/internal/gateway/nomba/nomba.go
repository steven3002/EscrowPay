// Package nomba implements gateway.Gateway against the Nomba payments API
// (https://developer.nomba.com). Funding links are hosted checkout orders paid
// into the configured sub-account; payouts and refunds are bank transfers out
// of that sub-account. Authentication uses the parent account id in the
// accountId header on every call, with money operations scoped to the
// sub-account.
//
// Nomba does not enforce idempotency on transfer references server-side in
// every environment, so this adapter never resubmits an ambiguous request; the
// caller's settlement-leg claim protocol plus payout webhooks provide the
// exactly-once guarantee.
package nomba

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// userAgent is sent on every request. The API sits behind a WAF that rejects
// default non-browser library user agents outright, so a browser-equivalent
// string is required for any call to succeed.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// Beneficiary is a Nigerian bank account a transfer pays into.
type Beneficiary struct {
	AccountNumber string
	BankCode      string
	AccountName   string
}

func (b Beneficiary) complete() bool {
	return b.AccountNumber != "" && b.BankCode != "" && b.AccountName != ""
}

// Config carries the credentials and policy an adapter instance needs. All
// values come from environment configuration; nothing here is persisted.
type Config struct {
	// BaseURL selects the environment: https://sandbox.nomba.com for test
	// credentials, https://api.nomba.com for production. Paths are identical
	// on both hosts.
	BaseURL string
	// ClientID and ClientSecret authenticate the token issue call.
	ClientID     string
	ClientSecret string
	// AccountID is the parent account id, sent as the accountId header on
	// every request.
	AccountID string
	// SubAccountID is the sub-account that receives checkout funds and
	// sources transfers.
	SubAccountID string
	// PublicBaseURL is the app origin payers are redirected back to after a
	// hosted checkout (the pocket page).
	PublicBaseURL string
	// FallbackCustomerEmail is used when a pocket's buyer has no email on
	// file; the checkout API requires one.
	FallbackCustomerEmail string
	// PayoutBeneficiary is the default transfer destination for settlement
	// legs, used until participants carry their own bank accounts.
	PayoutBeneficiary Beneficiary
	// RefundBeneficiary receives transfer refunds; falls back to
	// PayoutBeneficiary when unset.
	RefundBeneficiary Beneficiary

	// HTTPClient overrides the default 30-second-timeout client (tests).
	HTTPClient *http.Client
	Logger     *slog.Logger
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// Client is the Nomba adapter. Construct it with New; it is safe for
// concurrent use.
type Client struct {
	cfg    Config
	http   *http.Client
	logger *slog.Logger
	now    func() time.Time

	mu  sync.Mutex
	tok token
}

// New validates cfg and builds a Client.
func New(cfg Config) (*Client, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("nomba: client id and secret are required")
	}
	if cfg.AccountID == "" || cfg.SubAccountID == "" {
		return nil, errors.New("nomba: parent account id and sub-account id are required")
	}
	if cfg.BaseURL == "" {
		return nil, errors.New("nomba: base URL is required")
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.PublicBaseURL == "" {
		cfg.PublicBaseURL = "http://localhost:3000"
	}
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	if cfg.FallbackCustomerEmail == "" {
		cfg.FallbackCustomerEmail = "buyer@escrowpay.example"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{cfg: cfg, http: httpClient, logger: logger, now: now}, nil
}

// nairaAmount renders kobo as the naira-decimal number the API expects,
// without passing through floating point.
func nairaAmount(kobo int64) string {
	if kobo < 0 {
		kobo = 0
	}
	return fmt.Sprintf("%d.%02d", kobo/100, kobo%100)
}
