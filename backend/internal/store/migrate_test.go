package store_test

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"escrowpay/internal/store"
)

// TestMigrateIsIdempotent applies the full migration set twice against a fresh
// database and confirms the second run is a clean no-op, then checks that the
// 0002 columns are present.
func TestMigrateIsIdempotent(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://escrowpay:escrowpay_dev@localhost:5433/escrowpay"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Skipf("integration Postgres not available: %v", err)
	}
	defer admin.Close(ctx)

	const dbName = "escrowpay_migrate_test"
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(c, base)
		if err != nil {
			return
		}
		defer conn.Close(c)
		_, _ = conn.Exec(c, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	dbURL := u.String()

	if err := store.Migrate(dbURL); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := store.Migrate(dbURL); err != nil {
		t.Fatalf("second migrate (should be no-op): %v", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	for _, col := range []string{"delivery_window_minutes", "funding_link_ref", "funding_link_url", "release_code_enc"} {
		var exists bool
		err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pockets' AND column_name=$1)`, col).
			Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("migration 0002 did not create pockets.%s", col)
		}
	}
}
