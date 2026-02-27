// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	embeddedmigrations "github.com/adiadia/agent-runtime/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaMigrationLockID int64 = 0x4152545f4d494752 // "ART_MIGR"

var requiredTables = []string{
	"api_keys",
	"runs",
	"steps",
	"events",
	"run_requests",
	"workflow_templates",
	"workflow_template_steps",
}

type requiredColumn struct {
	Table  string
	Column string
}

var requiredColumns = []requiredColumn{
	{Table: "api_keys", Column: "name"},
	{Table: "api_keys", Column: "token_hash"},
	{Table: "runs", Column: "priority"},
}

type SchemaHealthChecker struct {
	pool *pgxpool.Pool
}

func NewSchemaHealthChecker(pool *pgxpool.Pool) *SchemaHealthChecker {
	return &SchemaHealthChecker{pool: pool}
}

func (h *SchemaHealthChecker) Check(ctx context.Context) error {
	return SchemaReady(ctx, h.pool)
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	if pool == nil {
		return errors.New("nil database pool")
	}
	if logger == nil {
		logger = slog.Default()
	}

	started := time.Now()
	logger.Info("schema bootstrap starting")

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire db connection for schema bootstrap: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockID); err != nil {
		return fmt.Errorf("acquire schema bootstrap lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, schemaMigrationLockID); unlockErr != nil {
			logger.Error("schema bootstrap unlock failed", "error", unlockErr)
		}
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	migrations, err := embeddedmigrations.Ordered()
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	if len(migrations) == 0 {
		return errors.New("no embedded migrations found")
	}

	applied := 0
	skipped := 0

	for _, migration := range migrations {
		var alreadyApplied bool
		if err := conn.QueryRow(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)`,
			migration.Name,
		).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.Name, err)
		}

		if alreadyApplied {
			skipped++
			continue
		}

		logger.Info("applying migration", "file", migration.Name)
		if err := applyMigration(ctx, conn, migration); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.Name, err)
		}
		logger.Info("migration applied", "file", migration.Name)
		applied++
	}

	logger.Info("schema bootstrap complete",
		"applied", applied,
		"skipped", skipped,
		"duration_ms", time.Since(started).Milliseconds(),
	)

	return SchemaReady(ctx, pool)
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, migration embeddedmigrations.File) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_migrations (filename)
		VALUES ($1)
	`, migration.Name); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func SchemaReady(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil database pool")
	}

	missingTables := make([]string, 0, len(requiredTables))
	for _, table := range requiredTables {
		var relationName *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)`, "public."+table).Scan(&relationName); err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
		if relationName == nil || strings.TrimSpace(*relationName) == "" {
			missingTables = append(missingTables, table)
		}
	}
	if len(missingTables) > 0 {
		return fmt.Errorf("required tables missing: %s", strings.Join(missingTables, ", "))
	}

	missingColumns := make([]string, 0, len(requiredColumns))
	for _, column := range requiredColumns {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name = $1
				  AND column_name = $2
			)
		`, column.Table, column.Column).Scan(&exists); err != nil {
			return fmt.Errorf("check column %s.%s: %w", column.Table, column.Column, err)
		}
		if !exists {
			missingColumns = append(missingColumns, column.Table+"."+column.Column)
		}
	}
	if len(missingColumns) > 0 {
		return fmt.Errorf("required columns missing: %s", strings.Join(missingColumns, ", "))
	}

	return nil
}
