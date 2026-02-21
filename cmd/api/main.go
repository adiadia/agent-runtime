package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adiadia/agent-runtime/internal/config"
	"github.com/adiadia/agent-runtime/internal/persistence/postgres"
	"github.com/adiadia/agent-runtime/internal/repository"
	httptransport "github.com/adiadia/agent-runtime/internal/transport/http"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// Structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect failed: %v", err)
	}
	defer pool.Close()

	runRepo := repository.NewRunRepository(pool, logger)
	stepRepo := repository.NewStepRepository(pool, logger)

	handler := httptransport.NewRouter(httptransport.Deps{
		RunRepo:  runRepo,
		StepRepo: stepRepo,
		Logger:   logger,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("api listening", "addr", cfg.HTTPAddr)

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
