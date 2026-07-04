package nomba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"escrowpay/internal/gateway"
)

// fakeNomba is a scripted provider: it verifies the wire contract (paths,
// headers, body shapes) and counts calls so tests can assert exactly-once
// behavior at the HTTP level.
type fakeNomba struct {
	t              *testing.T
	mux            *http.ServeMux
	issued         atomic.Int64
	orders         atomic.Int64
	transfers      atomic.Int64
	transferStatus string
	failTransfer   int // HTTP status to fail transfers with; 0 = succeed
}

func newFakeNomba(t *testing.T) (*fakeNomba, *httptest.Server) {
	t.Helper()
	f := &fakeNomba{t: t, mux: http.NewServeMux(), transferStatus: "SUCCESS"}

	f.mux.HandleFunc("POST /v1/auth/token/issue", func(w http.ResponseWriter, r *http.Request) {
		f.checkCommonHeaders(r, false)
		var body map[string]string
		f.decode(r, &body)
		if body["grant_type"] != "client_credentials" || body["client_id"] == "" || body["client_secret"] == "" {
			f.t.Errorf("token issue body = %v", body)
		}
		n := f.issued.Add(1)
		f.respond(w, map[string]any{
			"access_token":  fmt.Sprintf("tok-%d", n),
			"refresh_token": fmt.Sprintf("ref-%d", n),
			"expiresAt":     "2026-07-02 12:30:00",
			"businessId":    "parent-acct",
		})
	})

	f.mux.HandleFunc("POST /v1/checkout/order", func(w http.ResponseWriter, r *http.Request) {
		f.checkCommonHeaders(r, true)
		var body struct {
			Order checkoutOrder `json:"order"`
		}
		f.decode(r, &body)
		if body.Order.AccountID != "sub-acct" {
			f.t.Errorf("order accountId = %q, want the sub-account", body.Order.AccountID)
		}
		if !strings.HasPrefix(body.Order.OrderReference, "escrowpay-") {
			f.t.Errorf("orderReference = %q", body.Order.OrderReference)
		}
		if body.Order.Amount.String() != "10200.00" {
			f.t.Errorf("amount = %q, want 10200.00", body.Order.Amount.String())
		}
		if body.Order.Currency != "NGN" || body.Order.CustomerEmail == "" || !strings.Contains(body.Order.CallbackURL, "/p/") {
			f.t.Errorf("order fields = %+v", body.Order)
		}
		// The live API generates its own order reference rather than echoing
		// the submitted one; the fake mirrors that.
		n := f.orders.Add(1)
		f.respond(w, map[string]any{
			"checkoutLink":   "https://checkout.test/pay/" + body.Order.OrderReference,
			"orderReference": fmt.Sprintf("provider-ref-%d", n),
		})
	})

	f.mux.HandleFunc("POST /v2/transfers/bank/{sub}", func(w http.ResponseWriter, r *http.Request) {
		f.checkCommonHeaders(r, true)
		if r.PathValue("sub") != "sub-acct" {
			f.t.Errorf("transfer path sub-account = %q", r.PathValue("sub"))
		}
		if f.failTransfer != 0 {
			w.WriteHeader(f.failTransfer)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "92", "description": "scripted failure"})
			return
		}
		var body transferRequest
		f.decode(r, &body)
		if body.MerchantTxRef == "" || body.AccountNumber == "" || body.BankCode == "" || body.AccountName == "" {
			f.t.Errorf("transfer body = %+v", body)
		}
		n := f.transfers.Add(1)
		f.respond(w, map[string]any{
			"id":     fmt.Sprintf("API-TRANSFER-%d", n),
			"status": f.transferStatus,
		})
	})

	srv := httptest.NewServer(f.mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeNomba) checkCommonHeaders(r *http.Request, wantBearer bool) {
	if r.Header.Get("accountId") != "parent-acct" {
		f.t.Errorf("%s: accountId header = %q, want the parent account", r.URL.Path, r.Header.Get("accountId"))
	}
	if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Mozilla") {
		f.t.Errorf("%s: user agent %q is not browser-like", r.URL.Path, ua)
	}
	if wantBearer && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
		f.t.Errorf("%s: authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
	}
}

func (f *fakeNomba) decode(r *http.Request, dst any) {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		f.t.Errorf("%s: decode body: %v", r.URL.Path, err)
	}
}

func (f *fakeNomba) respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": "00", "description": "Success", "data": data})
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:       baseURL,
		ClientID:      "cid",
		ClientSecret:  "csecret",
		AccountID:     "parent-acct",
		SubAccountID:  "sub-acct",
		PublicBaseURL: "http://localhost:3000",
		PayoutBeneficiary: Beneficiary{
			AccountNumber: "0000000000", BankCode: "058", AccountName: "Test Beneficiary",
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestCreateFundingLinkAndTokenReuse(t *testing.T) {
	f, srv := newFakeNomba(t)
	c := testClient(t, srv.URL)
	ctx := context.Background()

	expires := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	link, err := c.CreateFundingLink(ctx, gateway.CreateFundingLinkRequest{
		PocketID:   "8b6f2c1d-0000-4000-8000-123456789abc",
		ShortCode:  "abc123xy",
		AmountKobo: 1020000,
		ExpiresAt:  expires,
	})
	if err != nil {
		t.Fatalf("create funding link: %v", err)
	}
	if link.Ref != "provider-ref-1" {
		t.Errorf("ref = %q, want the provider-generated reference", link.Ref)
	}
	if !strings.HasPrefix(link.URL, "https://checkout.test/pay/") {
		t.Errorf("url = %q", link.URL)
	}
	if !link.ExpiresAt.Equal(expires) {
		t.Errorf("expiresAt = %v", link.ExpiresAt)
	}

	// A second operation inside the token's lifetime reuses the cached token.
	if _, err := c.CreateFundingLink(ctx, gateway.CreateFundingLinkRequest{
		PocketID: "8b6f2c1d-0000-4000-8000-123456789abc", ShortCode: "abc123xy", AmountKobo: 1020000,
	}); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if got := f.issued.Load(); got != 1 {
		t.Errorf("token issues = %d, want 1", got)
	}
	if got := f.orders.Load(); got != 2 {
		t.Errorf("orders = %d, want 2", got)
	}
}

func TestPayoutCarriesMerchantTxRef(t *testing.T) {
	f, srv := newFakeNomba(t)
	c := testClient(t, srv.URL)

	ref, err := c.Payout(context.Background(), gateway.PayoutRequest{
		PocketID:        "8b6f2c1d-0000-4000-8000-123456789abc",
		BeneficiaryRole: "vendor",
		AmountKobo:      1000000,
		IdempotencyKey:  "8b6f2c1d:payout:vendor",
	})
	if err != nil {
		t.Fatalf("payout: %v", err)
	}
	if ref != "API-TRANSFER-1" {
		t.Errorf("ref = %q", ref)
	}
	if got := f.transfers.Load(); got != 1 {
		t.Errorf("transfers = %d, want 1", got)
	}
}

func TestPayoutPendingBillingIsAccepted(t *testing.T) {
	f, srv := newFakeNomba(t)
	f.transferStatus = "PENDING_BILLING"
	c := testClient(t, srv.URL)

	if _, err := c.Payout(context.Background(), gateway.PayoutRequest{
		AmountKobo: 50000, IdempotencyKey: "k1",
	}); err != nil {
		t.Fatalf("pending billing should be accepted: %v", err)
	}
}

func TestPayoutRefundStatusIsRejected(t *testing.T) {
	f, srv := newFakeNomba(t)
	f.transferStatus = "REFUND"
	c := testClient(t, srv.URL)

	_, err := c.Payout(context.Background(), gateway.PayoutRequest{AmountKobo: 50000, IdempotencyKey: "k1"})
	if !errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("REFUND status: err = %v, want ErrRejected", err)
	}
}

func TestTransferHTTPRejectionIsErrRejected(t *testing.T) {
	f, srv := newFakeNomba(t)
	f.failTransfer = http.StatusBadRequest
	c := testClient(t, srv.URL)

	_, err := c.Payout(context.Background(), gateway.PayoutRequest{AmountKobo: 50000, IdempotencyKey: "k1"})
	if !errors.Is(err, gateway.ErrRejected) {
		t.Fatalf("400: err = %v, want ErrRejected", err)
	}
}

func TestTransferServerErrorIsAmbiguous(t *testing.T) {
	f, srv := newFakeNomba(t)
	f.failTransfer = http.StatusBadGateway
	c := testClient(t, srv.URL)

	_, err := c.Payout(context.Background(), gateway.PayoutRequest{AmountKobo: 50000, IdempotencyKey: "k1"})
	if err == nil || errors.Is(err, gateway.ErrRejected) || errors.Is(err, gateway.ErrNotSubmitted) {
		t.Fatalf("5xx must stay ambiguous, got %v", err)
	}
}

func TestMissingBeneficiaryFailsBeforeSubmission(t *testing.T) {
	f, srv := newFakeNomba(t)
	c, err := New(Config{
		BaseURL: srv.URL, ClientID: "cid", ClientSecret: "csecret",
		AccountID: "parent-acct", SubAccountID: "sub-acct",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Payout(context.Background(), gateway.PayoutRequest{AmountKobo: 50000, IdempotencyKey: "k1"})
	if !errors.Is(err, gateway.ErrNotSubmitted) {
		t.Fatalf("missing beneficiary: err = %v, want ErrNotSubmitted", err)
	}
	if got := f.transfers.Load(); got != 0 {
		t.Errorf("transfers = %d, want 0 (nothing submitted)", got)
	}
}

func TestStaleTokenRetriesOnceWith401(t *testing.T) {
	f, srv := newFakeNomba(t)
	var rejected atomic.Bool
	// Wrap the transfer route: the first authenticated attempt is rejected as
	// stale, forcing one re-issue and one retry.
	f.mux.HandleFunc("POST /v2/transfers/bank/stale/{sub}", func(w http.ResponseWriter, r *http.Request) {})
	orig := f.mux
	wrapped := http.NewServeMux()
	wrapped.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/transfers/") && rejected.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "401", "description": "expired token"})
			return
		}
		orig.ServeHTTP(w, r)
	})
	srv.Config.Handler = wrapped

	c := testClient(t, srv.URL)
	if _, err := c.Payout(context.Background(), gateway.PayoutRequest{AmountKobo: 50000, IdempotencyKey: "k1"}); err != nil {
		t.Fatalf("payout after stale token: %v", err)
	}
	if got := f.issued.Load(); got != 2 {
		t.Errorf("token issues = %d, want 2 (initial + refresh after 401)", got)
	}
	if got := f.transfers.Load(); got != 1 {
		t.Errorf("transfers = %d, want exactly 1", got)
	}
}

func TestNairaAmountFormatting(t *testing.T) {
	cases := map[int64]string{
		0:       "0.00",
		5:       "0.05",
		100:     "1.00",
		1020000: "10200.00",
		999:     "9.99",
	}
	for kobo, want := range cases {
		if got := nairaAmount(kobo); got != want {
			t.Errorf("nairaAmount(%d) = %q, want %q", kobo, got, want)
		}
	}
}
