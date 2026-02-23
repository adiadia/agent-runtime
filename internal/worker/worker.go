// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/adiadia/agent-runtime/internal/metrics"
	execs "github.com/adiadia/agent-runtime/internal/worker/executors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool               *pgxpool.Pool
	Logger             *slog.Logger
	ReclaimAfter       time.Duration
	MaxAttempts        int
	RetryBaseDelay     time.Duration
	DefaultStepTimeout time.Duration
	APIKeyID           uuid.UUID
}

type Worker struct {
	pool               *pgxpool.Pool
	logger             *slog.Logger
	httpClient         *http.Client
	reclaimAfter       time.Duration
	executors          map[domain.StepName]StepExecutor
	maxAttempts        int
	retryBaseDelay     time.Duration
	defaultStepTimeout time.Duration
	apiKeyID           uuid.UUID
}

func New(deps Deps) *Worker {
	l := deps.Logger
	if l == nil {
		l = slog.Default()
	}

	reclaim := deps.ReclaimAfter
	if reclaim <= 0 {
		reclaim = 5 * time.Minute
	}

	maxAtt := deps.MaxAttempts
	if maxAtt <= 0 {
		maxAtt = 3
	}

	retryBase := deps.RetryBaseDelay
	if retryBase <= 0 {
		retryBase = 2 * time.Second
	}

	defaultStepTimeout := deps.DefaultStepTimeout
	if defaultStepTimeout <= 0 {
		defaultStepTimeout = 30 * time.Second
	}

	registry := map[domain.StepName]StepExecutor{
		domain.StepLLM:  &execs.LLMExecutor{},
		domain.StepTool: &execs.ToolExecutor{},
	}

	return &Worker{
		pool:               deps.Pool,
		logger:             l,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
		reclaimAfter:       reclaim,
		maxAttempts:        maxAtt,
		retryBaseDelay:     retryBase,
		defaultStepTimeout: defaultStepTimeout,
		executors:          registry,
		apiKeyID:           deps.APIKeyID,
	}
}

type claimedStep struct {
	StepID  uuid.UUID
	RunID   uuid.UUID
	Name    domain.StepName
	Status  domain.StepStatus
	Timeout time.Duration
}

func (w *Worker) ProcessOnce(ctx context.Context) error {
	claimStart := time.Now()
	step, err := w.claimOneStep(ctx)
	metrics.ObserveWorkerClaimLatency(time.Since(claimStart))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		w.logger.Error("claim step failed", "error", err)
		return err
	}

	w.logger.Info("step claimed",
		"api_key_id", w.apiKeyID,
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
		"prev_status", step.Status,
		"timeout", step.Timeout,
	)

	w.logger.Info("executing step",
		"api_key_id", w.apiKeyID,
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
		"timeout", step.Timeout,
	)

	out, costUSD, execErr := w.executeStep(ctx, step)
	if execErr != nil {
		timeoutTriggered := errors.Is(execErr, context.DeadlineExceeded)
		w.logger.Error("step execution failed",
			"run_id", step.RunID,
			"step_id", step.StepID,
			"step", step.Name,
			"timeout", step.Timeout,
			"timeout_triggered", timeoutTriggered,
			"error", execErr,
		)
		return w.markStepFailed(ctx, step.StepID, execErr)
	}

	if err := w.markStepSucceeded(ctx, step, out, costUSD); err != nil {
		w.logger.Error("mark step succeeded failed",
			"run_id", step.RunID,
			"step_id", step.StepID,
			"step", step.Name,
			"cost_usd", costUSD,
			"error", err,
		)
		return err
	}

	w.logger.Info("step completed",
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
		"cost_usd", costUSD,
		"timeout", step.Timeout,
		"timeout_triggered", false,
	)

	return nil
}

// claimOneStep claims one runnable step.
// It also supports "reclaiming" stuck RUNNING steps older than reclaimAfter.
func (w *Worker) claimOneStep(ctx context.Context) (claimedStep, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return claimedStep{}, err
	}
	defer tx.Rollback(ctx)

	reclaimBefore := time.Now().Add(-w.reclaimAfter)

	var maxConcurrency int
	if err := tx.QueryRow(ctx,
		`SELECT max_concurrent_runs FROM api_keys WHERE id=$1`,
		w.apiKeyID,
	).Scan(&maxConcurrency); err != nil {
		return claimedStep{}, err
	}
	if maxConcurrency <= 0 {
		maxConcurrency = domain.DefaultMaxConcurrentRuns
	}

	var runningSteps int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM steps st
		JOIN runs r ON st.run_id = r.id
		WHERE r.api_key_id = $1
		  AND st.status = $2
	`,
		w.apiKeyID,
		domain.StepRunning,
	).Scan(&runningSteps); err != nil {
		return claimedStep{}, err
	}
	if runningSteps >= maxConcurrency {
		w.logger.Debug("claim skipped by concurrency limit",
			"api_key_id", w.apiKeyID,
			"running_steps", runningSteps,
			"max_concurrency", maxConcurrency,
		)
		return claimedStep{}, pgx.ErrNoRows
	}

	var (
		s              claimedStep
		nameStr        string
		timeoutSeconds sql.NullInt64
	)

	err = tx.QueryRow(ctx, `
		SELECT st.id, st.run_id, st.name, st.status, st.timeout_seconds
		FROM steps st
		JOIN runs r ON st.run_id = r.id
		WHERE (
			st.status = $1 OR
			(st.status = $2 AND st.started_at IS NOT NULL AND st.started_at < $3)
		)
		  AND (st.next_run_at IS NULL OR st.next_run_at <= NOW())
		  AND st.name <> $4
		  AND r.status NOT IN ($5,$6,$7)
		  AND r.api_key_id = $9
		  AND NOT EXISTS (
			SELECT 1 FROM steps s2
			WHERE s2.run_id = st.run_id
			  AND s2.created_at < st.created_at
			  AND s2.status <> $8
		  )
		ORDER BY r.priority DESC, st.created_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`,
		domain.StepPending,
		domain.StepRunning,
		reclaimBefore,
		domain.StepApproval,
		domain.RunCanceled,
		domain.RunFailed,
		domain.RunSuccess,
		domain.StepSuccess,
		w.apiKeyID,
	).Scan(&s.StepID, &s.RunID, &nameStr, &s.Status, &timeoutSeconds)

	if err != nil {
		return claimedStep{}, err
	}

	s.Name = domain.StepName(nameStr)
	s.Timeout = resolveStepTimeout(timeoutSeconds, w.defaultStepTimeout)

	// Validate step name to avoid corrupted DB values
	switch s.Name {
	case domain.StepLLM, domain.StepTool, domain.StepApproval:
	default:
		return claimedStep{}, errors.New("invalid step name in DB: " + nameStr)
	}

	// Build input JSON for this step
	inputPayload, _ := json.Marshal(map[string]any{
		"step":      s.Name,
		"claimedAt": time.Now(),
		"reclaimed": s.Status == domain.StepRunning,
	})

	// Mark RUNNING and increment attempts (every claim counts as an attempt)
	_, err = tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    started_at=COALESCE(started_at, NOW()),
		    input=$3::jsonb,
		    next_run_at=NULL,
		    attempts = attempts + 1
		WHERE id=$1
	`,
		s.StepID,
		domain.StepRunning,
		inputPayload,
	)
	if err != nil {
		return claimedStep{}, err
	}

	// Mark run RUNNING if it was PENDING
	runStatusUpdated, _ := tx.Exec(ctx, `
		UPDATE runs
		SET status=$2, updated_at=NOW()
		WHERE id=$1 AND status=$3
	`,
		s.RunID,
		domain.RunRunning,
		domain.RunPending,
	)

	if err := insertStepEvent(ctx, tx, s.RunID, s.StepID, "STEP_CLAIMED", map[string]any{
		"status":     domain.StepRunning,
		"step":       s.Name,
		"reclaimed":  s.Status == domain.StepRunning,
		"previous":   s.Status,
		"api_key_id": w.apiKeyID,
		"claimed_at": time.Now().UTC(),
	}); err != nil {
		return claimedStep{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return claimedStep{}, err
	}

	if runStatusUpdated.RowsAffected() > 0 {
		metrics.IncRunStatus(string(domain.RunRunning))
	}

	w.logger.Info("step marked running",
		"api_key_id", w.apiKeyID,
		"run_id", s.RunID,
		"step_id", s.StepID,
		"step", s.Name,
		"reclaimed", s.Status == domain.StepRunning,
	)

	return s, nil
}

func (w *Worker) executeStep(ctx context.Context, s claimedStep) (json.RawMessage, float64, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveStepExecutionDuration(time.Since(start))
	}()

	executor, ok := w.executors[s.Name]
	if !ok {
		return nil, 0, errors.New("no executor registered for step: " + string(s.Name))
	}

	execCtx := ctx
	cancel := func() {}
	if s.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, s.Timeout)
	}
	defer cancel()

	return executor.Execute(execCtx, s.RunID)
}

func (w *Worker) markStepSucceeded(ctx context.Context, step claimedStep, output json.RawMessage, costUSD float64) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    output=$3::jsonb,
		    cost_usd=$4,
		    next_run_at=NULL,
		    finished_at=NOW()
		WHERE id=$1
	`,
		step.StepID,
		domain.StepSuccess,
		output,
		costUSD,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE runs
		SET total_cost_usd = total_cost_usd + $2
		WHERE id=$1
	`,
		step.RunID,
		costUSD,
	)
	if err != nil {
		return err
	}

	if err := insertStepEvent(ctx, tx, step.RunID, step.StepID, "STEP_SUCCEEDED", map[string]any{
		"status": domain.StepSuccess,
		"step":   step.Name,
		"cost":   costUSD,
	}); err != nil {
		return err
	}

	// If TOOL finished -> move APPROVAL to WAITING_APPROVAL
	if step.Name == domain.StepTool {
		var approvalStepID uuid.UUID
		err = tx.QueryRow(ctx, `
			UPDATE steps
			SET status=$2
			WHERE run_id=$1
			  AND name=$3
			  AND status=$4
			RETURNING id
		`,
			step.RunID,
			domain.StepWaiting,
			domain.StepApproval,
			domain.StepPending,
		).Scan(&approvalStepID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if err == nil {
			if err := insertStepEvent(ctx, tx, step.RunID, approvalStepID, "STEP_WAITING_APPROVAL", map[string]any{
				"status": domain.StepWaiting,
				"step":   domain.StepApproval,
			}); err != nil {
				return err
			}
		}
	}

	// If all steps are SUCCEEDED -> mark run SUCCEEDED
	var (
		runTerminal   bool
		webhookURL    sql.NullString
		webhookSecret sql.NullString
		runFinishedAt time.Time
	)

	err = tx.QueryRow(ctx, `
		UPDATE runs r
		SET status=$2, updated_at=NOW()
		WHERE r.id=$1
		  AND NOT EXISTS (
			SELECT 1 FROM steps s
			WHERE s.run_id=r.id AND s.status <> $3
		  )
		RETURNING r.webhook_url, r.webhook_secret, r.updated_at
	`,
		step.RunID,
		domain.RunSuccess,
		domain.StepSuccess,
	).Scan(&webhookURL, &webhookSecret, &runFinishedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		runTerminal = true
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	metrics.IncStepStatus(string(domain.StepSuccess))
	if runTerminal {
		metrics.IncRunStatus(string(domain.RunSuccess))
		w.deliverTerminalWebhook(
			ctx,
			step.RunID,
			domain.RunSuccess,
			runFinishedAt.UTC(),
			webhookURL.String,
			webhookSecret.String,
		)
	}

	w.logger.Info("step marked succeeded",
		"api_key_id", w.apiKeyID,
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
		"cost_usd", costUSD,
	)

	return nil
}

// markStepFailed retries up to maxAttempts.
// - if attempts < maxAttempts: set step back to PENDING (retry)
// - else: set step FAILED and mark run FAILED
func (w *Worker) markStepFailed(ctx context.Context, stepID uuid.UUID, execErr error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Read attempts + run_id
	var attempts int
	var runID uuid.UUID

	if err := tx.QueryRow(ctx, `
		SELECT attempts, run_id
		FROM steps
		WHERE id=$1
	`, stepID).Scan(&attempts, &runID); err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]string{
		"error": execErr.Error(),
	})

	// Retry if attempts < maxAttempts
	if attempts < w.maxAttempts {
		nextRunAt := time.Now().UTC().Add(backoffDelay(w.retryBaseDelay, attempts))

		w.logger.Warn("step failed - retrying",
			"step_id", stepID,
			"run_id", runID,
			"attempt", attempts,
			"max_attempts", w.maxAttempts,
			"next_run_at", nextRunAt,
		)

		_, err = tx.Exec(ctx, `
			UPDATE steps
			SET status=$2,
			    output=$3::jsonb,
			    next_run_at=$4,
			    finished_at=NOW()
			WHERE id=$1
		`,
			stepID,
			domain.StepPending,
			payload,
			nextRunAt,
		)
		if err != nil {
			return err
		}

		if err := insertStepEvent(ctx, tx, runID, stepID, "STEP_FAILED_RETRY", map[string]any{
			"status":       domain.StepPending,
			"error":        execErr.Error(),
			"attempt":      attempts,
			"max_attempts": w.maxAttempts,
			"next_run_at":  nextRunAt,
		}); err != nil {
			return err
		}

		if err := tx.Commit(ctx); err != nil {
			return err
		}

		metrics.IncStepRetries()
		w.logger.Info("retry scheduled",
			"api_key_id", w.apiKeyID,
			"step_id", stepID,
			"run_id", runID,
			"attempt", attempts,
			"next_run_at", nextRunAt,
		)
		return nil
	}

	// Permanently fail
	w.logger.Error("step permanently failed",
		"step_id", stepID,
		"run_id", runID,
		"attempts", attempts,
		"max_attempts", w.maxAttempts,
	)

	_, err = tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    output=$3::jsonb,
		    next_run_at=NULL,
		    finished_at=NOW()
		WHERE id=$1
	`,
		stepID,
		domain.StepFailed,
		payload,
	)
	if err != nil {
		return err
	}

	if err := insertStepEvent(ctx, tx, runID, stepID, "STEP_FAILED", map[string]any{
		"status":       domain.StepFailed,
		"error":        execErr.Error(),
		"attempt":      attempts,
		"max_attempts": w.maxAttempts,
	}); err != nil {
		return err
	}

	var (
		runTerminal   bool
		webhookURL    sql.NullString
		webhookSecret sql.NullString
		runFinishedAt time.Time
	)

	err = tx.QueryRow(ctx, `
		UPDATE runs
		SET status=$2, updated_at=NOW()
		WHERE id=$1
		  AND status <> $2
		RETURNING webhook_url, webhook_secret, updated_at
	`,
		runID,
		domain.RunFailed,
	).Scan(&webhookURL, &webhookSecret, &runFinishedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		runTerminal = true
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	metrics.IncStepStatus(string(domain.StepFailed))
	if runTerminal {
		metrics.IncRunStatus(string(domain.RunFailed))
		w.deliverTerminalWebhook(
			ctx,
			runID,
			domain.RunFailed,
			runFinishedAt.UTC(),
			webhookURL.String,
			webhookSecret.String,
		)
	}

	w.logger.Error("step marked failed",
		"api_key_id", w.apiKeyID,
		"step_id", stepID,
		"run_id", runID,
		"attempts", attempts,
	)

	return nil
}

func backoffDelay(base time.Duration, attempts int) time.Duration {
	if base <= 0 {
		base = 2 * time.Second
	}
	if attempts <= 0 {
		return base
	}

	delay := base
	const maxDuration = time.Duration(1<<63 - 1)

	for i := 0; i < attempts; i++ {
		if delay > maxDuration/2 {
			return maxDuration
		}
		delay *= 2
	}

	return delay
}

func resolveStepTimeout(timeoutSeconds sql.NullInt64, defaultTimeout time.Duration) time.Duration {
	if timeoutSeconds.Valid && timeoutSeconds.Int64 > 0 {
		return time.Duration(timeoutSeconds.Int64) * time.Second
	}

	if defaultTimeout <= 0 {
		return 30 * time.Second
	}

	return defaultTimeout
}

func insertStepEvent(
	ctx context.Context,
	tx pgx.Tx,
	runID uuid.UUID,
	stepID uuid.UUID,
	eventType string,
	payload any,
) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO events (id, run_id, step_id, type, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`,
		uuid.New(),
		runID,
		stepID,
		eventType,
		payloadJSON,
	)
	return err
}
