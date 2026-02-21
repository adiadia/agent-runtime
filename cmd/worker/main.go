package main

import (
	"context"
	"log"
	"time"

	"github.com/adiadia/agent-runtime/internal/config"
	"github.com/adiadia/agent-runtime/internal/persistence/postgres"
	"github.com/adiadia/agent-runtime/internal/worker"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect failed: %v", err)
	}
	defer pool.Close()

	w := worker.New(worker.Deps{
		Pool: pool,
	})

	log.Println("worker started")

	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C
		if err := w.ProcessOnce(ctx); err != nil {
			log.Printf("worker process failed: %v", err)
		}
	}
}
