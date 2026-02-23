// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ENV", "")
	t.Setenv("ADMIN_TOKEN", "")

	cfg := Load()

	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("expected default HTTPAddr=:8080, got %s", cfg.HTTPAddr)
	}
	if cfg.DatabaseURL != "postgres://durable:durable@localhost:5432/durable?sslmode=disable" {
		t.Fatalf("expected default DatabaseURL, got %s", cfg.DatabaseURL)
	}
	if cfg.Env != "dev" {
		t.Fatalf("expected default Env=dev, got %s", cfg.Env)
	}
	if cfg.AdminToken != "" {
		t.Fatalf("expected default AdminToken to be empty, got %s", cfg.AdminToken)
	}
}

func TestLoadRespectsEnv(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/app?sslmode=disable")
	t.Setenv("ENV", "prod")
	t.Setenv("ADMIN_TOKEN", "master-token")

	cfg := Load()
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected HTTP_ADDR override, got %s", cfg.HTTPAddr)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/app?sslmode=disable" {
		t.Fatalf("expected DatabaseURL override, got %s", cfg.DatabaseURL)
	}
	if cfg.Env != "prod" {
		t.Fatalf("expected ENV override, got %s", cfg.Env)
	}
	if cfg.AdminToken != "master-token" {
		t.Fatalf("expected ADMIN_TOKEN override, got %s", cfg.AdminToken)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("EXAMPLE_KEY", "value")
	if got := getenv("EXAMPLE_KEY", "fallback"); got != "value" {
		t.Fatalf("expected env value, got %s", got)
	}

	t.Setenv("EXAMPLE_KEY", "")
	if got := getenv("EXAMPLE_KEY", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback value, got %s", got)
	}
}
