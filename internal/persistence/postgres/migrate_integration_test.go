//go:build integration

// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/adiadia/agent-runtime/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEnsureSchemaBootstrapsEmptyDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	baseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set DATABASE_URL to run integration tests")
	}

	adminPool, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("skip integration test: cannot create admin pool (%v)", err)
	}
	defer adminPool.Close()

	if err := adminPool.Ping(ctx); err != nil {
		t.Skipf("skip integration test: cannot reach database (%v)", err)
	}

	testDBName := "bootstrap_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{testDBName}.Sanitize()); err != nil {
		t.Skipf("skip integration test: cannot create database (%v)", err)
	}

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()

		_, _ = adminPool.Exec(cleanupCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1
			  AND pid <> pg_backend_pid()
		`, testDBName)
		if _, err := adminPool.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{testDBName}.Sanitize()); err != nil {
			t.Logf("cleanup warning: drop temp database failed (%v)", err)
		}
	}()

	poolCfg, err := pgxpool.ParseConfig(baseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	poolCfg.ConnConfig.Database = testDBName

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("create temp database pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping temp database: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := EnsureSchema(ctx, pool, logger); err != nil {
		t.Fatalf("ensure schema first run: %v", err)
	}
	if err := EnsureSchema(ctx, pool, logger); err != nil {
		t.Fatalf("ensure schema second run: %v", err)
	}
	if err := SchemaReady(ctx, pool); err != nil {
		t.Fatalf("schema ready check: %v", err)
	}

	apiKeys := repository.NewAPIKeyRepository(pool, logger)
	created, err := apiKeys.CreateAPIKey(ctx, domain.CreateAPIKeyParams{Name: "bootstrap-test"})
	if err != nil {
		t.Fatalf("create api key after bootstrap: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected created api key ID")
	}
	if created.Token == "" {
		t.Fatal("expected created api key token")
	}
}
