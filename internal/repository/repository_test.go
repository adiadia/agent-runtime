// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewRunRepository(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var pool *pgxpool.Pool

	repo := NewRunRepository(pool, logger)
	if repo == nil {
		t.Fatal("expected run repository instance")
	}
	if repo.pool != pool {
		t.Fatal("expected pool reference to be preserved")
	}
	if repo.logger != logger {
		t.Fatal("expected logger reference to be preserved")
	}
}

func TestNewStepRepository(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var pool *pgxpool.Pool

	repo := NewStepRepository(pool, logger)
	if repo == nil {
		t.Fatal("expected step repository instance")
	}
	if repo.pool != pool {
		t.Fatal("expected pool reference to be preserved")
	}
	if repo.logger != logger {
		t.Fatal("expected logger reference to be preserved")
	}
}
