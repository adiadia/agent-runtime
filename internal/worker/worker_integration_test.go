//go:build integration

// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/adiadia/agent-runtime/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerSchedulesExponentialBackoffRetry(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	w := New(Deps{
		Pool:           pool,
		Logger:         logger,
		APIKeyID:       apiKeyID,
		ReclaimAfter:   5 * time.Minute,
		MaxAttempts:    4,
		RetryBaseDelay: 1 * time.Second,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM: failingExecutor{err: errors.New("boom")},
	}

	start := time.Now().UTC()
	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once #1: %v", err)
	}

	var (
		attempts1  int
		status1    domain.StepStatus
		nextRunAt1 time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, next_run_at
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM).Scan(&status1, &attempts1, &nextRunAt1); err != nil {
		t.Fatalf("read first retry state: %v", err)
	}

	if status1 != domain.StepPending {
		t.Fatalf("expected first failed step status %s got %s", domain.StepPending, status1)
	}
	if attempts1 != 1 {
		t.Fatalf("expected attempts=1 after first failure got %d", attempts1)
	}
	if !nextRunAt1.After(start.Add(1500 * time.Millisecond)) {
		t.Fatalf("expected next_run_at to be delayed by backoff, got %s", nextRunAt1)
	}

	// Retry must not be claimed until next_run_at is reached.
	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once before next_run_at: %v", err)
	}

	var attemptsAfterBlockedClaim int
	if err := pool.QueryRow(ctx, `
		SELECT attempts
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM).Scan(&attemptsAfterBlockedClaim); err != nil {
		t.Fatalf("read attempts after blocked claim: %v", err)
	}
	if attemptsAfterBlockedClaim != 1 {
		t.Fatalf("expected attempts to remain 1 before next_run_at, got %d", attemptsAfterBlockedClaim)
	}

	// Force the scheduled retry to become due, then fail again.
	if _, err := pool.Exec(ctx, `
		UPDATE steps
		SET next_run_at=NOW() - INTERVAL '1 second'
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM); err != nil {
		t.Fatalf("force next_run_at due: %v", err)
	}

	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once #2: %v", err)
	}

	var (
		attempts2  int
		status2    domain.StepStatus
		nextRunAt2 time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, next_run_at
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM).Scan(&status2, &attempts2, &nextRunAt2); err != nil {
		t.Fatalf("read second retry state: %v", err)
	}

	if status2 != domain.StepPending {
		t.Fatalf("expected second failed step status %s got %s", domain.StepPending, status2)
	}
	if attempts2 != 2 {
		t.Fatalf("expected attempts=2 after second failure got %d", attempts2)
	}
	if nextRunAt2.Sub(time.Now().UTC()) < 3*time.Second {
		t.Fatalf("expected second retry delay to be exponential (>=3s remaining), got next_run_at=%s", nextRunAt2)
	}
}

func TestWorkerUsesDefaultStepTimeoutWhenDBTimeoutIsNull(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	w := New(Deps{
		Pool:               pool,
		Logger:             logger,
		APIKeyID:           apiKeyID,
		ReclaimAfter:       5 * time.Minute,
		MaxAttempts:        3,
		RetryBaseDelay:     1 * time.Second,
		DefaultStepTimeout: 100 * time.Millisecond,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM: timeoutExecutor{},
	}

	start := time.Now()
	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("expected default timeout to stop step quickly, elapsed=%s", elapsed)
	}

	var (
		status    domain.StepStatus
		attempts  int
		nextRunAt time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, next_run_at
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM).Scan(&status, &attempts, &nextRunAt); err != nil {
		t.Fatalf("query step after timeout: %v", err)
	}

	if status != domain.StepPending {
		t.Fatalf("expected timed-out step to be retried as %s, got %s", domain.StepPending, status)
	}
	if attempts != 1 {
		t.Fatalf("expected attempts=1 after timeout, got %d", attempts)
	}
	if !nextRunAt.After(time.Now().UTC()) {
		t.Fatalf("expected next_run_at to be scheduled in future, got %s", nextRunAt)
	}
}

func TestDedicatedWorkerStaysWithinTenant(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyA, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key A: %v", err)
	}
	apiKeyB, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key B: %v", err)
	}

	ctxA := auth.WithAPIKeyID(ctx, apiKeyA)
	ctxB := auth.WithAPIKeyID(ctx, apiKeyB)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	runA, err := runRepo.CreateRun(ctxA, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run for key A: %v", err)
	}
	runB, err := runRepo.CreateRun(ctxB, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run for key B: %v", err)
	}

	w := New(Deps{
		Pool:         pool,
		Logger:       logger,
		APIKeyID:     apiKeyA,
		ReclaimAfter: 5 * time.Minute,
		MaxAttempts:  3,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM:  staticExecutor{payload: json.RawMessage(`{"ok":"llm"}`)},
		domain.StepTool: staticExecutor{payload: json.RawMessage(`{"ok":"tool"}`)},
	}

	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once #1: %v", err)
	}
	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once #2: %v", err)
	}

	var processedA int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM steps
		WHERE run_id=$1 AND status <> $2
	`, runA, domain.StepPending).Scan(&processedA); err != nil {
		t.Fatalf("query tenant A steps: %v", err)
	}
	if processedA == 0 {
		t.Fatal("expected dedicated worker to process at least one step for tenant A")
	}

	var processedB int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM steps
		WHERE run_id=$1 AND status <> $2
	`, runB, domain.StepPending).Scan(&processedB); err != nil {
		t.Fatalf("query tenant B steps: %v", err)
	}
	if processedB != 0 {
		t.Fatalf("expected tenant B steps to remain pending, got %d processed", processedB)
	}

	var claimedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE run_id=$1 AND type='STEP_CLAIMED'
	`, runA).Scan(&claimedEvents); err != nil {
		t.Fatalf("query claimed events: %v", err)
	}
	if claimedEvents == 0 {
		t.Fatal("expected at least one STEP_CLAIMED event for tenant A run")
	}

	var successEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE run_id=$1 AND type='STEP_SUCCEEDED'
	`, runA).Scan(&successEvents); err != nil {
		t.Fatalf("query success events: %v", err)
	}
	if successEvents == 0 {
		t.Fatal("expected at least one STEP_SUCCEEDED event for tenant A run")
	}

	var waitingEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE run_id=$1 AND type='STEP_WAITING_APPROVAL'
	`, runA).Scan(&waitingEvents); err != nil {
		t.Fatalf("query waiting approval events: %v", err)
	}
	if waitingEvents != 1 {
		t.Fatalf("expected exactly one STEP_WAITING_APPROVAL event for tenant A run, got %d", waitingEvents)
	}
}

func TestWorkerTracksStepAndRunCosts(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Pre-approve so worker can complete the run after TOOL succeeds.
	if _, err := pool.Exec(ctx, `
		UPDATE steps
		SET status=$2
		WHERE run_id=$1 AND name=$3
	`, runID, domain.StepSuccess, domain.StepApproval); err != nil {
		t.Fatalf("pre-approve run: %v", err)
	}

	w := New(Deps{
		Pool:         pool,
		Logger:       logger,
		APIKeyID:     apiKeyID,
		ReclaimAfter: 5 * time.Minute,
		MaxAttempts:  3,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM:  staticExecutor{payload: json.RawMessage(`{"ok":"llm"}`), costUSD: 1.25},
		domain.StepTool: staticExecutor{payload: json.RawMessage(`{"ok":"tool"}`), costUSD: 0.75},
	}

	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process llm step: %v", err)
	}
	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process tool step: %v", err)
	}

	var llmCost float64
	if err := pool.QueryRow(ctx, `
		SELECT cost_usd::double precision
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM).Scan(&llmCost); err != nil {
		t.Fatalf("query llm step cost: %v", err)
	}
	if llmCost != 1.25 {
		t.Fatalf("expected llm cost 1.25 got %f", llmCost)
	}

	var toolCost float64
	if err := pool.QueryRow(ctx, `
		SELECT cost_usd::double precision
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepTool).Scan(&toolCost); err != nil {
		t.Fatalf("query tool step cost: %v", err)
	}
	if toolCost != 0.75 {
		t.Fatalf("expected tool cost 0.75 got %f", toolCost)
	}

	var totalCost float64
	if err := pool.QueryRow(ctx, `
		SELECT total_cost_usd::double precision
		FROM runs
		WHERE id=$1
	`, runID).Scan(&totalCost); err != nil {
		t.Fatalf("query run total cost: %v", err)
	}
	if totalCost != 2.0 {
		t.Fatalf("expected total run cost 2.0 got %f", totalCost)
	}
}

func TestWorkerClaimsHigherPriorityRunFirst(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	lowPriorityRun, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{Priority: 0})
	if err != nil {
		t.Fatalf("create low priority run: %v", err)
	}
	highPriorityRun, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{Priority: 10})
	if err != nil {
		t.Fatalf("create high priority run: %v", err)
	}

	w := New(Deps{
		Pool:         pool,
		Logger:       logger,
		APIKeyID:     apiKeyID,
		ReclaimAfter: 5 * time.Minute,
		MaxAttempts:  3,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM:  staticExecutor{payload: json.RawMessage(`{"ok":"llm"}`)},
		domain.StepTool: staticExecutor{payload: json.RawMessage(`{"ok":"tool"}`)},
	}

	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once: %v", err)
	}

	var highStatus domain.StepStatus
	if err := pool.QueryRow(ctx, `
		SELECT status
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, highPriorityRun, domain.StepLLM).Scan(&highStatus); err != nil {
		t.Fatalf("query high priority run step status: %v", err)
	}
	if highStatus == domain.StepPending {
		t.Fatalf("expected high priority run to be processed first, step status=%s", highStatus)
	}

	var lowStatus domain.StepStatus
	if err := pool.QueryRow(ctx, `
		SELECT status
		FROM steps
		WHERE run_id=$1 AND name=$2
	`, lowPriorityRun, domain.StepLLM).Scan(&lowStatus); err != nil {
		t.Fatalf("query low priority run step status: %v", err)
	}
	if lowStatus != domain.StepPending {
		t.Fatalf("expected low priority run step to remain pending, got %s", lowStatus)
	}
}

func TestDedicatedWorkerRespectsConcurrentRunningStepLimit(t *testing.T) {
	ctx := context.Background()
	pool := workerIntegrationPool(t, ctx)
	defer pool.Close()

	if err := workerTruncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := workerCreateAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE api_keys
		SET max_concurrent_runs=1
		WHERE id=$1
	`, apiKeyID); err != nil {
		t.Fatalf("set max_concurrent_runs: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := repository.NewRunRepository(pool, logger)

	runA, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run A: %v", err)
	}
	runB, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run B: %v", err)
	}

	// Simulate an already-running step for this tenant.
	if _, err := pool.Exec(ctx, `
		UPDATE steps
		SET status=$2, started_at=NOW()
		WHERE run_id=$1 AND name=$3
	`, runA, domain.StepRunning, domain.StepLLM); err != nil {
		t.Fatalf("mark step running: %v", err)
	}

	w := New(Deps{
		Pool:         pool,
		Logger:       logger,
		APIKeyID:     apiKeyID,
		ReclaimAfter: 5 * time.Minute,
		MaxAttempts:  3,
	})
	w.executors = map[domain.StepName]StepExecutor{
		domain.StepLLM:  staticExecutor{payload: json.RawMessage(`{"ok":"llm"}`)},
		domain.StepTool: staticExecutor{payload: json.RawMessage(`{"ok":"tool"}`)},
	}

	if err := w.ProcessOnce(ctx); err != nil {
		t.Fatalf("process once: %v", err)
	}

	var runBProcessed int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM steps
		WHERE run_id=$1 AND status <> $2
	`, runB, domain.StepPending).Scan(&runBProcessed); err != nil {
		t.Fatalf("query run B step statuses: %v", err)
	}
	if runBProcessed != 0 {
		t.Fatalf("expected run B steps to remain pending due concurrent limit, got %d processed", runBProcessed)
	}
}

type staticExecutor struct {
	payload json.RawMessage
	costUSD float64
}

func (s staticExecutor) Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error) {
	return s.payload, s.costUSD, nil
}

type failingExecutor struct {
	err error
}

func (f failingExecutor) Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error) {
	return nil, 0, f.err
}

type timeoutExecutor struct{}

func (e timeoutExecutor) Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error) {
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

func workerTruncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE TABLE events, steps, run_requests, runs, api_keys RESTART IDENTITY CASCADE`)
	return err
}

func workerCreateAPIKey(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	id := uuid.New()
	token := uuid.NewString()
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	_, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, name, token_hash)
		VALUES ($1, $2, $3)
	`, id, "worker-"+id.String()[:8], tokenHash)
	return id, err
}

func workerIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set DATABASE_URL to run integration tests")
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("skip integration test: cannot create pgx pool (%v)", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skip integration test: cannot reach database (%v)", err)
	}

	return pool
}
