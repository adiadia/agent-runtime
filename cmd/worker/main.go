// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"log"
	"strings"
	"time"

	"github.com/adiadia/agent-runtime/internal/config"
	"github.com/adiadia/agent-runtime/internal/logging"
	"github.com/adiadia/agent-runtime/internal/persistence/postgres"
	"github.com/adiadia/agent-runtime/internal/worker"
	"github.com/google/uuid"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	cfg := config.Load()
	logger := logging.NewLogger(cfg.Env)

	var (
		apiKeyIDFlag       string
		pollInterval       time.Duration
		maxAttempts        int
		reclaimAfter       time.Duration
		retryBaseDelay     time.Duration
		defaultStepTimeout time.Duration
	)
	flag.StringVar(&apiKeyIDFlag, "api-key-id", "", "API key UUID for dedicated worker (required)")
	flag.DurationVar(&pollInterval, "poll-interval", 250*time.Millisecond, "worker poll interval")
	flag.IntVar(&maxAttempts, "max-attempts", 3, "max execution attempts per step")
	flag.DurationVar(&reclaimAfter, "reclaim-after", 5*time.Minute, "reclaim running steps older than this duration")
	flag.DurationVar(&retryBaseDelay, "retry-base-delay", 2*time.Second, "base delay for exponential retry backoff")
	flag.DurationVar(&defaultStepTimeout, "default-step-timeout", 30*time.Second, "default timeout for steps with NULL timeout_seconds")
	flag.Parse()

	if strings.TrimSpace(apiKeyIDFlag) == "" {
		log.Fatal("worker requires --api-key-id for dedicated mode")
	}
	apiKeyID, err := uuid.Parse(apiKeyIDFlag)
	if err != nil {
		log.Fatalf("invalid --api-key-id: %v", err)
	}
	if pollInterval <= 0 {
		log.Fatal("--poll-interval must be > 0")
	}
	if maxAttempts <= 0 {
		log.Fatal("--max-attempts must be > 0")
	}
	if reclaimAfter <= 0 {
		log.Fatal("--reclaim-after must be > 0")
	}
	if retryBaseDelay <= 0 {
		log.Fatal("--retry-base-delay must be > 0")
	}
	if defaultStepTimeout <= 0 {
		log.Fatal("--default-step-timeout must be > 0")
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect failed: %v", err)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := postgres.EnsureSchema(ctx, pool, logger); err != nil {
			log.Fatalf("schema bootstrap failed: %v", err)
		}
	} else {
		logger.Info("auto schema bootstrap disabled", "env_var", "AUTO_MIGRATE")
	}

	w := worker.New(worker.Deps{
		Pool:               pool,
		Logger:             logger,
		APIKeyID:           apiKeyID,
		ReclaimAfter:       reclaimAfter,
		MaxAttempts:        maxAttempts,
		RetryBaseDelay:     retryBaseDelay,
		DefaultStepTimeout: defaultStepTimeout,
	})

	logger.Info("worker started",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate,
		"api_key_id", apiKeyID,
		"poll_interval", pollInterval,
		"max_attempts", maxAttempts,
		"reclaim_after", reclaimAfter,
		"retry_base_delay", retryBaseDelay,
		"default_step_timeout", defaultStepTimeout,
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		<-ticker.C
		if err := w.ProcessOnce(ctx); err != nil {
			logger.Error("worker process failed", "error", err)
		}
	}
}
