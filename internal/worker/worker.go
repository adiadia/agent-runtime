package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	execs "github.com/adiadia/agent-runtime/internal/worker/executors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	ReclaimAfter time.Duration
	MaxAttempts  int
}

type Worker struct {
	pool         *pgxpool.Pool
	logger       *slog.Logger
	reclaimAfter time.Duration
	executors    map[domain.StepName]StepExecutor
	maxAttempts  int
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

	registry := map[domain.StepName]StepExecutor{
		domain.StepLLM:  &execs.LLMExecutor{},
		domain.StepTool: &execs.ToolExecutor{},
	}

	return &Worker{
		pool:         deps.Pool,
		logger:       l,
		reclaimAfter: reclaim,
		maxAttempts:  maxAtt,
		executors:    registry,
	}
}

type claimedStep struct {
	StepID uuid.UUID
	RunID  uuid.UUID
	Name   domain.StepName
	Status domain.StepStatus
}

func (w *Worker) ProcessOnce(ctx context.Context) error {
	step, err := w.claimOneStep(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		w.logger.Error("claim step failed", "error", err)
		return err
	}

	w.logger.Info("step claimed",
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
		"prev_status", step.Status,
	)

	out, execErr := w.executeStep(ctx, step)
	if execErr != nil {
		w.logger.Error("step execution failed",
			"run_id", step.RunID,
			"step_id", step.StepID,
			"step", step.Name,
			"error", execErr,
		)
		return w.markStepFailed(ctx, step.StepID, execErr)
	}

	if err := w.markStepSucceeded(ctx, step, out); err != nil {
		w.logger.Error("mark step succeeded failed",
			"run_id", step.RunID,
			"step_id", step.StepID,
			"step", step.Name,
			"error", err,
		)
		return err
	}

	w.logger.Info("step completed",
		"run_id", step.RunID,
		"step_id", step.StepID,
		"step", step.Name,
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

	var (
		s       claimedStep
		nameStr string
	)

	err = tx.QueryRow(ctx, `
		SELECT st.id, st.run_id, st.name, st.status
		FROM steps st
		JOIN runs r ON st.run_id = r.id
		WHERE (
			st.status = $1 OR
			(st.status = $2 AND st.started_at IS NOT NULL AND st.started_at < $3)
		)
		  AND st.name <> $4
		  AND r.status NOT IN ($5,$6,$7)
		  AND NOT EXISTS (
			SELECT 1 FROM steps s2
			WHERE s2.run_id = st.run_id
			  AND s2.created_at < st.created_at
			  AND s2.status <> $8
		  )
		ORDER BY st.created_at ASC
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
	).Scan(&s.StepID, &s.RunID, &nameStr, &s.Status)

	if err != nil {
		return claimedStep{}, err
	}

	s.Name = domain.StepName(nameStr)

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
	_, _ = tx.Exec(ctx, `
		UPDATE runs
		SET status=$2, updated_at=NOW()
		WHERE id=$1 AND status=$3
	`,
		s.RunID,
		domain.RunRunning,
		domain.RunPending,
	)

	return s, tx.Commit(ctx)
}

func (w *Worker) executeStep(ctx context.Context, s claimedStep) (json.RawMessage, error) {
	executor, ok := w.executors[s.Name]
	if !ok {
		return nil, errors.New("no executor registered for step: " + string(s.Name))
	}
	return executor.Execute(ctx, s.RunID)
}

func (w *Worker) markStepSucceeded(ctx context.Context, step claimedStep, output json.RawMessage) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    output=$3::jsonb,
		    finished_at=NOW()
		WHERE id=$1
	`,
		step.StepID,
		domain.StepSuccess,
		output,
	)
	if err != nil {
		return err
	}

	// If TOOL finished -> move APPROVAL to WAITING_APPROVAL
	if step.Name == domain.StepTool {
		_, err = tx.Exec(ctx, `
			UPDATE steps
			SET status=$2
			WHERE run_id=$1
			  AND name=$3
			  AND status=$4
		`,
			step.RunID,
			domain.StepWaiting,
			domain.StepApproval,
			domain.StepPending,
		)
		if err != nil {
			return err
		}
	}

	// If all steps are SUCCEEDED -> mark run SUCCEEDED
	_, err = tx.Exec(ctx, `
		UPDATE runs r
		SET status=$2, updated_at=NOW()
		WHERE r.id=$1
		  AND NOT EXISTS (
			SELECT 1 FROM steps s
			WHERE s.run_id=r.id AND s.status <> $3
		  )
	`,
		step.RunID,
		domain.RunSuccess,
		domain.StepSuccess,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
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
		w.logger.Warn("step failed - retrying",
			"step_id", stepID,
			"run_id", runID,
			"attempt", attempts,
			"max_attempts", w.maxAttempts,
		)

		_, err = tx.Exec(ctx, `
			UPDATE steps
			SET status=$2,
			    output=$3::jsonb,
			    finished_at=NOW()
			WHERE id=$1
		`,
			stepID,
			domain.StepPending,
			payload,
		)
		if err != nil {
			return err
		}

		return tx.Commit(ctx)
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

	_, _ = tx.Exec(ctx, `
		UPDATE runs
		SET status=$2, updated_at=NOW()
		WHERE id=$1
	`,
		runID,
		domain.RunFailed,
	)

	return tx.Commit(ctx)
}
