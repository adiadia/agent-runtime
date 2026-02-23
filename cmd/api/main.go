// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adiadia/agent-runtime/internal/config"
	"github.com/adiadia/agent-runtime/internal/logging"
	"github.com/adiadia/agent-runtime/internal/persistence/postgres"
	"github.com/adiadia/agent-runtime/internal/repository"
	httptransport "github.com/adiadia/agent-runtime/internal/transport/http"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	logger := logging.NewLogger(cfg.Env)

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect failed: %v", err)
	}
	defer pool.Close()

	runRepo := repository.NewRunRepository(pool, logger)
	stepRepo := repository.NewStepRepository(pool, logger)
	eventRepo := repository.NewEventRepository(pool, logger)
	apiKeyRepo := repository.NewAPIKeyRepository(pool, logger)

	handler := httptransport.NewRouter(httptransport.Deps{
		RunRepo:        runRepo,
		StepRepo:       stepRepo,
		EventRepo:      eventRepo,
		APIKeyAdmin:    apiKeyRepo,
		Logger:         logger,
		APIKeyResolver: apiKeyRepo,
		AdminToken:     cfg.AdminToken,
		Version:        Version,
		Commit:         Commit,
		BuildDate:      BuildDate,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("api listening",
			"addr", cfg.HTTPAddr,
			"version", Version,
			"commit", Commit,
			"build_date", BuildDate,
		)

		if err := srv.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}
}
