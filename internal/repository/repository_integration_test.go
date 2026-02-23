//go:build integration

// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunAndStepRepositoriesIntegration(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)
	stepRepo := NewStepRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	status, err := runRepo.GetRun(tenantCtx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if status != domain.RunPending {
		t.Fatalf("expected run status %s got %s", domain.RunPending, status)
	}

	steps, err := stepRepo.ListSteps(tenantCtx, runID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps got %d", len(steps))
	}

	expectedNames := []string{
		string(domain.StepLLM),
		string(domain.StepTool),
		string(domain.StepApproval),
	}
	for i := range expectedNames {
		if steps[i].Name != expectedNames[i] {
			t.Fatalf("expected step[%d] name %s got %s", i, expectedNames[i], steps[i].Name)
		}
		if steps[i].Status != string(domain.StepPending) {
			t.Fatalf("expected step[%d] status %s got %s", i, domain.StepPending, steps[i].Status)
		}
	}

	if err := runRepo.CancelRun(tenantCtx, runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	status, err = runRepo.GetRun(tenantCtx, runID)
	if err != nil {
		t.Fatalf("get run after cancel: %v", err)
	}
	if status != domain.RunCanceled {
		t.Fatalf("expected run status %s got %s", domain.RunCanceled, status)
	}
}

func TestApproveRunIntegration(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	_, err = pool.Exec(ctx, `
		UPDATE steps
		SET status=$2
		WHERE run_id=$1 AND name=$3
	`,
		runID,
		domain.StepWaiting,
		domain.StepApproval,
	)
	if err != nil {
		t.Fatalf("set approval step waiting: %v", err)
	}

	if err := runRepo.ApproveRun(tenantCtx, runID); err != nil {
		t.Fatalf("approve run: %v", err)
	}

	var approvalStatus domain.StepStatus
	err = pool.QueryRow(ctx, `
		SELECT status
		FROM steps
		WHERE run_id=$1 AND name=$2
	`,
		runID,
		domain.StepApproval,
	).Scan(&approvalStatus)
	if err != nil {
		t.Fatalf("query approval step status: %v", err)
	}

	if approvalStatus != domain.StepSuccess {
		t.Fatalf("expected approval step status %s got %s", domain.StepSuccess, approvalStatus)
	}

	var events int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE run_id=$1 AND type='RUN_APPROVED'
	`, runID).Scan(&events)
	if err != nil {
		t.Fatalf("query run approved events: %v", err)
	}
	if events != 1 {
		t.Fatalf("expected 1 RUN_APPROVED event got %d", events)
	}
}

func TestRepositoryEnforcesRunOwnership(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyA, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key A: %v", err)
	}
	apiKeyB, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key B: %v", err)
	}

	ctxA := auth.WithAPIKeyID(ctx, apiKeyA)
	ctxB := auth.WithAPIKeyID(ctx, apiKeyB)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)
	stepRepo := NewStepRepository(pool, logger)

	runID, err := runRepo.CreateRun(ctxA, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if _, err := runRepo.GetRun(ctxB, runID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for GetRun with wrong tenant, got %v", err)
	}

	if _, err := stepRepo.ListSteps(ctxB, runID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for ListSteps with wrong tenant, got %v", err)
	}

	if err := runRepo.CancelRun(ctxB, runID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for CancelRun with wrong tenant, got %v", err)
	}

	if err := runRepo.ApproveRun(ctxB, runID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for ApproveRun with wrong tenant, got %v", err)
	}
}

func TestCreateRunRespectsMaxConcurrentRuns(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE api_keys
		SET max_concurrent_runs=1
		WHERE id=$1
	`, apiKeyID); err != nil {
		t.Fatalf("set api key max_concurrent_runs: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create first run: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE runs
		SET status=$2
		WHERE id=$1
	`, runID, domain.RunRunning); err != nil {
		t.Fatalf("mark first run running: %v", err)
	}

	if _, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{}); !errors.Is(err, domain.ErrMaxConcurrentRunsExceeded) {
		t.Fatalf("expected ErrMaxConcurrentRunsExceeded, got %v", err)
	}
}

func TestCreateRunWithSameIdempotencyKeyReturnsSameRunID(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	idempotentCtx := auth.WithIdempotencyKey(tenantCtx, "idem-same-key")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)

	firstRunID, err := runRepo.CreateRun(idempotentCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create first run: %v", err)
	}

	secondRunID, err := runRepo.CreateRun(idempotentCtx, domain.CreateRunParams{})
	if err != nil {
		t.Fatalf("create second run: %v", err)
	}

	if firstRunID != secondRunID {
		t.Fatalf("expected same run id for repeated idempotency key, got %s and %s", firstRunID, secondRunID)
	}

	var runsCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runs
		WHERE api_key_id=$1
	`, apiKeyID).Scan(&runsCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runsCount != 1 {
		t.Fatalf("expected exactly 1 run row, got %d", runsCount)
	}

	var reqCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run_requests
		WHERE api_key_id=$1 AND idempotency_key=$2
	`, apiKeyID, "idem-same-key").Scan(&reqCount); err != nil {
		t.Fatalf("count run_requests: %v", err)
	}
	if reqCount != 1 {
		t.Fatalf("expected exactly 1 run_requests row, got %d", reqCount)
	}
}

func TestCreateRunPersistsWebhookURLAndRunCostBreakdown(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{
		WebhookURL: "https://example.com/hook",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	var webhookURL string
	if err := pool.QueryRow(ctx, `
		SELECT webhook_url
		FROM runs
		WHERE id=$1
	`, runID).Scan(&webhookURL); err != nil {
		t.Fatalf("query webhook url: %v", err)
	}
	if webhookURL != "https://example.com/hook" {
		t.Fatalf("expected webhook_url to persist, got %q", webhookURL)
	}

	// simulate billed costs
	if _, err := pool.Exec(ctx, `
		UPDATE steps
		SET cost_usd = 1.250000
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepLLM); err != nil {
		t.Fatalf("update step cost llm: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE steps
		SET cost_usd = 0.750000
		WHERE run_id=$1 AND name=$2
	`, runID, domain.StepTool); err != nil {
		t.Fatalf("update step cost tool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE runs
		SET total_cost_usd = 2.000000
		WHERE id=$1
	`, runID); err != nil {
		t.Fatalf("update run total cost: %v", err)
	}

	breakdown, err := runRepo.GetRunCost(tenantCtx, runID)
	if err != nil {
		t.Fatalf("get run cost: %v", err)
	}
	if breakdown.RunID != runID {
		t.Fatalf("expected run id %s got %s", runID, breakdown.RunID)
	}
	if breakdown.TotalCostUSD != 2.0 {
		t.Fatalf("expected total cost 2.0 got %f", breakdown.TotalCostUSD)
	}
	if len(breakdown.Steps) != 3 {
		t.Fatalf("expected 3 step costs got %d", len(breakdown.Steps))
	}
}

func TestCreateRunUsesWorkflowTemplateAndPriority(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	templateID := uuid.New()
	templateName := "custom-template-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_templates (id, name)
		VALUES ($1, $2)
	`, templateID, templateName); err != nil {
		t.Fatalf("insert workflow template: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_template_steps (id, template_id, position, name)
		VALUES
			($1, $2, 1, $3),
			($4, $2, 2, $5)
	`,
		uuid.New(),
		templateID,
		domain.StepTool,
		uuid.New(),
		domain.StepLLM,
	); err != nil {
		t.Fatalf("insert workflow template steps: %v", err)
	}

	apiKeyID, err := createIntegrationAPIKey(ctx, pool)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	tenantCtx := auth.WithAPIKeyID(ctx, apiKeyID)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runRepo := NewRunRepository(pool, logger)
	stepRepo := NewStepRepository(pool, logger)

	runID, err := runRepo.CreateRun(tenantCtx, domain.CreateRunParams{
		Priority:     9,
		TemplateName: templateName,
	})
	if err != nil {
		t.Fatalf("create run with custom template: %v", err)
	}

	var priority int
	if err := pool.QueryRow(ctx, `
		SELECT priority FROM runs WHERE id=$1
	`, runID).Scan(&priority); err != nil {
		t.Fatalf("query run priority: %v", err)
	}
	if priority != 9 {
		t.Fatalf("expected run priority 9 got %d", priority)
	}

	steps, err := stepRepo.ListSteps(tenantCtx, runID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps from custom template got %d", len(steps))
	}
	if steps[0].Name != string(domain.StepTool) {
		t.Fatalf("expected first step %s got %s", domain.StepTool, steps[0].Name)
	}
	if steps[1].Name != string(domain.StepLLM) {
		t.Fatalf("expected second step %s got %s", domain.StepLLM, steps[1].Name)
	}
}

func TestAPIKeyLifecycleRepositoryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	defer pool.Close()

	if err := truncateAll(ctx, pool); err != nil {
		t.Skipf("skip integration test: database not reachable (%v)", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKeyRepo := NewAPIKeyRepository(pool, logger)

	created, err := apiKeyRepo.CreateAPIKey(ctx, domain.CreateAPIKeyParams{
		Name:              "integration-key",
		MaxConcurrentRuns: 7,
		MaxRequestsPerMin: 70,
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatalf("expected created api key id")
	}
	if len(created.Token) <= len("sk_live_") || created.Token[:8] != "sk_live_" {
		t.Fatalf("expected token prefix sk_live_, got %q", created.Token)
	}

	var storedHash string
	if err := pool.QueryRow(ctx, `
		SELECT token_hash
		FROM api_keys
		WHERE id=$1
	`, created.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query token hash: %v", err)
	}

	sum := sha256.Sum256([]byte(created.Token))
	expectedHash := hex.EncodeToString(sum[:])
	if storedHash != expectedHash {
		t.Fatalf("expected token hash %s got %s", expectedHash, storedHash)
	}
	if storedHash == created.Token {
		t.Fatalf("raw token must not be stored")
	}

	resolved, found, err := apiKeyRepo.ResolveAPIKey(ctx, created.Token)
	if err != nil {
		t.Fatalf("resolve api key: %v", err)
	}
	if !found {
		t.Fatalf("expected api key to resolve by raw token")
	}
	if resolved.ID != created.ID {
		t.Fatalf("expected resolved id %s got %s", created.ID, resolved.ID)
	}

	keys, err := apiKeyRepo.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("list api keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 api key got %d", len(keys))
	}
	if keys[0].ID != created.ID {
		t.Fatalf("expected listed key %s got %s", created.ID, keys[0].ID)
	}

	if err := apiKeyRepo.RevokeAPIKey(ctx, created.ID); err != nil {
		t.Fatalf("revoke api key: %v", err)
	}

	_, found, err = apiKeyRepo.ResolveAPIKey(ctx, created.Token)
	if err != nil {
		t.Fatalf("resolve revoked api key: %v", err)
	}
	if found {
		t.Fatalf("expected revoked api key to be unresolved")
	}
}

func truncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE TABLE events, steps, run_requests, runs, api_keys RESTART IDENTITY CASCADE`)
	return err
}

func createIntegrationAPIKey(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	id := uuid.New()
	token := uuid.NewString()
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	_, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, name, token_hash)
		VALUES ($1, $2, $3)
	`, id, "integration-"+id.String()[:8], tokenHash)
	return id, err
}

func integrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
