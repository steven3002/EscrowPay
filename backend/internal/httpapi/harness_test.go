package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"escrowpay/internal/evidence"
	"escrowpay/internal/gateway/mock"
	"escrowpay/internal/httpapi"
	"escrowpay/internal/linktoken"
	"escrowpay/internal/notify/logstub"
	"escrowpay/internal/pocketapp"
	"escrowpay/internal/settlement"
	"escrowpay/internal/store"
)

// testPool is the connection pool to the throwaway integration database. It is
// nil when no Postgres is reachable, in which case every test skips.
var testPool *pgxpool.Pool

const (
	testReleaseCodeSecret = "test-release-code-secret"
	testLinkTokenSecret   = "test-link-token-secret"
	testDBName            = "escrowpay_s3_test"
)

var fixedNow = time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

// testClock is a mutable, concurrency-safe clock for driving timer-based
// transitions in tests without real sleeps. It starts at fixedNow.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestMain(m *testing.M) {
	if err := setupDB(); err != nil {
		log.Printf("integration Postgres unavailable, skipping store/httpapi tests: %v", err)
		os.Exit(m.Run())
	}
	code := m.Run()
	testPool.Close()
	teardownDB()
	os.Exit(code)
}

func baseDatabaseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://escrowpay:escrowpay_dev@localhost:5433/escrowpay"
}

func setupDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	base := baseDatabaseURL()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return err
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+testDBName+" WITH (FORCE)"); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+testDBName); err != nil {
		return err
	}

	testURL, err := withDBName(base, testDBName)
	if err != nil {
		return err
	}
	if err := store.Migrate(testURL); err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		return err
	}
	testPool = pool
	return nil
}

func teardownDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, baseDatabaseURL())
	if err != nil {
		return
	}
	defer admin.Close(ctx)
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+testDBName+" WITH (FORCE)")
}

func withDBName(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

// safeBuffer is a concurrency-safe sink for captured log output.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testEnv is a running API backed by the integration database, with a captured
// log stream and the mock gateway's call recorder exposed for assertions.
type testEnv struct {
	server  *httptest.Server
	gateway *mock.Gateway
	store   *store.Store
	minter  *linktoken.Minter
	logs    *safeBuffer
	clock   *testClock
	sweeper *settlement.Sweeper
}

func newTestEnv(t *testing.T) *testEnv { return newEnv(t, true) }

// newEnv builds a running API with the given sandbox mode, so tests can exercise
// both the demo-open surface and its non-sandbox rejection.
func newEnv(t *testing.T, sandbox bool) *testEnv {
	t.Helper()
	if testPool == nil {
		t.Skip("integration Postgres not available")
	}
	truncate(t)

	logs := &safeBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	clock := &testClock{t: fixedNow}
	gw := mock.New()
	repo := store.New(testPool, 72*time.Hour, 24*time.Hour, 60*time.Minute)
	minter := linktoken.NewMinter([]byte(testLinkTokenSecret))
	blobs, err := evidence.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("evidence store: %v", err)
	}
	app := pocketapp.New(pocketapp.Config{
		Store:                 repo,
		Gateway:               gw,
		Notifier:              logstub.New(logger),
		Minter:                minter,
		Evidence:              blobs,
		Logger:                logger,
		ReleaseCodeSecret:     []byte(testReleaseCodeSecret),
		FundingLinkTTL:        72 * time.Hour,
		GracePeriod:           24 * time.Hour,
		EvidenceCaptureWindow: 60 * time.Minute,
		EvidenceMaxBytes:      8 << 20,
		Sandbox:               sandbox,
		Now:                   clock.now,
	})

	mux := http.NewServeMux()
	httpapi.New(app, minter, logger).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{
		server:  srv,
		gateway: gw,
		store:   repo,
		minter:  minter,
		logs:    logs,
		clock:   clock,
		sweeper: settlement.NewSweeper(app, time.Minute, logger),
	}
}

// tick runs one sweeper pass at the current clock time and returns its report.
func (e *testEnv) tick(t *testing.T) settlement.Report {
	t.Helper()
	return e.sweeper.Tick(context.Background())
}

func truncate(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`TRUNCATE settlements, evidence, disputes, pocket_events, pocket_participants, pockets, users, webhook_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// req issues an HTTP request with an optional link token and JSON body and
// returns the status code and raw response body.
func (e *testEnv) req(t *testing.T, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		httpReq.Header.Set("X-Link-Token", token)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", string(data), err)
	}
	return v
}
