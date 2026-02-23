// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/adiadia/agent-runtime/internal/metrics"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

const defaultWorkflowTemplateName = "default"

func NewRunRepository(pool *pgxpool.Pool, logger *slog.Logger) *RunRepository {
	if logger == nil {
		logger = slog.Default()
	}

	return &RunRepository{
		pool:   pool,
		logger: logger,
	}
}

func (r *RunRepository) CreateRun(ctx context.Context, params domain.CreateRunParams) (uuid.UUID, error) {
	runID := uuid.New()
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("create run denied: missing api key id", "error", err)
		return uuid.Nil, err
	}
	idempotencyKey, hasIdempotencyKey := auth.IdempotencyKeyFromContext(ctx)
	webhookURL := strings.TrimSpace(params.WebhookURL)
	templateName := strings.TrimSpace(params.TemplateName)
	if templateName == "" {
		templateName = defaultWorkflowTemplateName
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	if hasIdempotencyKey {
		var existingRunID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT run_id
			FROM run_requests
			WHERE api_key_id=$1 AND idempotency_key=$2
		`, apiKeyID, idempotencyKey).Scan(&existingRunID)
		if err == nil {
			return existingRunID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			r.logger.Error("find idempotent run failed",
				"api_key_id", apiKeyID,
				"idempotency_key", idempotencyKey,
				"error", err,
			)
			return uuid.Nil, err
		}
	}

	var maxConcurrentRuns int
	if err := tx.QueryRow(ctx,
		`SELECT max_concurrent_runs FROM api_keys WHERE id=$1 FOR UPDATE`,
		apiKeyID,
	).Scan(&maxConcurrentRuns); err != nil {
		r.logger.Error("read api key limits failed", "api_key_id", apiKeyID, "error", err)
		return uuid.Nil, err
	}

	if maxConcurrentRuns <= 0 {
		maxConcurrentRuns = domain.DefaultMaxConcurrentRuns
	}

	var activeRuns int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runs
		WHERE api_key_id=$1
		  AND status IN ($2, $3)
	`,
		apiKeyID,
		domain.RunRunning,
		domain.RunWaiting,
	).Scan(&activeRuns); err != nil {
		r.logger.Error("count active runs failed", "api_key_id", apiKeyID, "error", err)
		return uuid.Nil, err
	}

	if activeRuns >= maxConcurrentRuns {
		r.logger.Warn("create run blocked by concurrent run limit",
			"api_key_id", apiKeyID,
			"active_runs", activeRuns,
			"max_concurrent_runs", maxConcurrentRuns,
		)
		return uuid.Nil, fmt.Errorf("%w: active=%d limit=%d", domain.ErrMaxConcurrentRunsExceeded, activeRuns, maxConcurrentRuns)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO runs (id, api_key_id, status, webhook_url, priority) VALUES ($1, $2, $3, $4, $5)`,
		runID, apiKeyID, domain.RunPending, nullString(webhookURL), params.Priority,
	)
	if err != nil {
		r.logger.Error("insert run failed", "run_id", runID, "api_key_id", apiKeyID, "error", err)
		return uuid.Nil, err
	}

	templateSteps, err := r.loadWorkflowTemplateSteps(ctx, tx, templateName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("%w: %s", domain.ErrWorkflowTemplateNotFound, templateName)
		}
		r.logger.Error("load workflow template failed",
			"template_name", templateName,
			"error", err,
		)
		return uuid.Nil, err
	}

	for _, step := range templateSteps {
		if _, err := tx.Exec(ctx,
			`INSERT INTO steps (id, run_id, name, status, timeout_seconds)
			 VALUES ($1, $2, $3, $4, $5)`,
			uuid.New(),
			runID,
			step.Name,
			domain.StepPending,
			nullInt64(step.TimeoutSeconds),
		); err != nil {
			r.logger.Error("insert step failed",
				"run_id", runID,
				"step", step.Name,
				"error", err,
			)
			return uuid.Nil, err
		}
	}

	if hasIdempotencyKey {
		_, err := tx.Exec(ctx, `
			INSERT INTO run_requests (id, api_key_id, idempotency_key, run_id)
			VALUES ($1, $2, $3, $4)
		`,
			uuid.New(),
			apiKeyID,
			idempotencyKey,
			runID,
		)
		if err != nil {
			// If another request won the same idempotency key race, return its run_id.
			if isUniqueViolation(err) {
				existingRunID, getErr := r.getRunIDByRequest(ctx, apiKeyID, idempotencyKey)
				if getErr != nil {
					r.logger.Error("fetch winner idempotent run failed",
						"api_key_id", apiKeyID,
						"idempotency_key", idempotencyKey,
						"error", getErr,
					)
					return uuid.Nil, getErr
				}
				return existingRunID, nil
			}

			r.logger.Error("insert run request failed",
				"api_key_id", apiKeyID,
				"idempotency_key", idempotencyKey,
				"run_id", runID,
				"error", err,
			)
			return uuid.Nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("commit failed", "run_id", runID, "error", err)
		return uuid.Nil, err
	}

	metrics.IncRunStatus(string(domain.RunPending))
	r.logger.Info("run created", "run_id", runID, "api_key_id", apiKeyID)
	return runID, nil
}

func (r *RunRepository) getRunIDByRequest(ctx context.Context, apiKeyID uuid.UUID, idempotencyKey string) (uuid.UUID, error) {
	var runID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT run_id
		FROM run_requests
		WHERE api_key_id=$1 AND idempotency_key=$2
	`,
		apiKeyID,
		idempotencyKey,
	).Scan(&runID)
	if err != nil {
		return uuid.Nil, err
	}
	return runID, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullInt64(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

type templateStep struct {
	Name           domain.StepName
	TimeoutSeconds sql.NullInt64
}

func (r *RunRepository) loadWorkflowTemplateSteps(ctx context.Context, tx pgx.Tx, templateName string) ([]templateStep, error) {
	rows, err := tx.Query(ctx, `
		SELECT wts.name, wts.timeout_seconds
		FROM workflow_templates wt
		JOIN workflow_template_steps wts ON wts.template_id = wt.id
		WHERE wt.name = $1
		ORDER BY wts.position ASC
	`, templateName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps := make([]templateStep, 0, 8)
	for rows.Next() {
		var (
			stepName string
			timeout  sql.NullInt64
		)
		if err := rows.Scan(&stepName, &timeout); err != nil {
			return nil, err
		}
		if strings.TrimSpace(stepName) == "" {
			return nil, errors.New("workflow template contains empty step name")
		}
		steps = append(steps, templateStep{
			Name:           domain.StepName(stepName),
			TimeoutSeconds: timeout,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, pgx.ErrNoRows
	}

	return steps, nil
}

func (r *RunRepository) GetRun(ctx context.Context, id uuid.UUID) (domain.RunStatus, error) {
	var status domain.RunStatus
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("get run denied: missing api key id", "run_id", id, "error", err)
		return "", err
	}

	err = r.pool.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1 AND api_key_id=$2`,
		id,
		apiKeyID,
	).Scan(&status)

	if err != nil {
		r.logger.Error("get run failed", "run_id", id, "api_key_id", apiKeyID, "error", err)
		return "", err
	}

	return status, nil
}

func (r *RunRepository) GetRunCost(ctx context.Context, id uuid.UUID) (domain.RunCostBreakdown, error) {
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("get run cost denied: missing api key id", "run_id", id, "error", err)
		return domain.RunCostBreakdown{}, err
	}

	var totalCostUSD float64
	if err := r.pool.QueryRow(ctx, `
		SELECT total_cost_usd::double precision
		FROM runs
		WHERE id=$1 AND api_key_id=$2
	`,
		id,
		apiKeyID,
	).Scan(&totalCostUSD); err != nil {
		r.logger.Error("get run total cost failed",
			"run_id", id,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return domain.RunCostBreakdown{}, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, name, status, cost_usd::double precision
		FROM steps
		WHERE run_id=$1
		ORDER BY created_at ASC
	`, id)
	if err != nil {
		r.logger.Error("get run step costs query failed",
			"run_id", id,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return domain.RunCostBreakdown{}, err
	}
	defer rows.Close()

	steps := make([]domain.StepCostBreakdown, 0, 4)
	for rows.Next() {
		var step domain.StepCostBreakdown
		if err := rows.Scan(&step.ID, &step.Name, &step.Status, &step.CostUSD); err != nil {
			r.logger.Error("scan run step costs failed",
				"run_id", id,
				"api_key_id", apiKeyID,
				"error", err,
			)
			return domain.RunCostBreakdown{}, err
		}
		steps = append(steps, step)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("iterate run step costs failed",
			"run_id", id,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return domain.RunCostBreakdown{}, err
	}

	return domain.RunCostBreakdown{
		RunID:        id,
		TotalCostUSD: totalCostUSD,
		Steps:        steps,
	}, nil
}

func (r *RunRepository) CancelRun(ctx context.Context, runID uuid.UUID) error {
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("cancel run denied: missing api key id", "run_id", runID, "error", err)
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return err
	}
	defer tx.Rollback(ctx)

	var status domain.RunStatus
	if err := tx.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1 AND api_key_id=$2`,
		runID,
		apiKeyID,
	).Scan(&status); err != nil {
		r.logger.Error("read run status failed", "run_id", runID, "api_key_id", apiKeyID, "error", err)
		return err
	}

	if status == domain.RunCanceled ||
		status == domain.RunSuccess ||
		status == domain.RunFailed {
		r.logger.Info("cancel skipped (terminal)",
			"run_id", runID,
			"status", status,
		)
		return tx.Commit(ctx)
	}

	_, err = tx.Exec(ctx,
		`UPDATE runs SET status=$2, updated_at=NOW() WHERE id=$1`,
		runID, domain.RunCanceled,
	)
	if err != nil {
		r.logger.Error("update run cancel failed", "run_id", runID, "error", err)
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    finished_at=COALESCE(finished_at, NOW())
		WHERE run_id=$1
		  AND status IN ($3,$4,$5)
	`,
		runID,
		domain.StepCanceled,
		domain.StepPending,
		domain.StepRunning,
		domain.StepWaiting,
	)
	if err != nil {
		r.logger.Error("update steps cancel failed", "run_id", runID, "error", err)
		return err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO events (id, run_id, type, payload)
		 VALUES ($1, $2, $3, $4)`,
		uuid.New(), runID, "RUN_CANCELED", `{"reason":"user_request"}`,
	)
	if err != nil {
		r.logger.Error("insert cancel event failed", "run_id", runID, "error", err)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("commit cancel failed", "run_id", runID, "error", err)
		return err
	}

	metrics.IncRunStatus(string(domain.RunCanceled))
	r.logger.Info("run canceled", "run_id", runID)
	return nil
}

func (r *RunRepository) ApproveRun(ctx context.Context, runID uuid.UUID) error {
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("approve run denied: missing api key id", "run_id", runID, "error", err)
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return err
	}
	defer tx.Rollback(ctx)

	var runStatus domain.RunStatus
	if err := tx.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1 AND api_key_id=$2`,
		runID,
		apiKeyID,
	).Scan(&runStatus); err != nil {
		r.logger.Error("read run status failed", "run_id", runID, "api_key_id", apiKeyID, "error", err)
		return err
	}

	if runStatus == domain.RunCanceled ||
		runStatus == domain.RunSuccess ||
		runStatus == domain.RunFailed {
		r.logger.Info("approve skipped (terminal)",
			"run_id", runID,
			"status", runStatus,
		)
		return tx.Commit(ctx)
	}

	var approvalStepID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE steps
		SET status=$2,
		    started_at=COALESCE(started_at, NOW()),
		    finished_at=COALESCE(finished_at, NOW())
		WHERE run_id=$1
		  AND name=$4
		  AND status=$3
		RETURNING id
	`,
		runID,
		domain.StepSuccess,
		domain.StepWaiting,
		domain.StepApproval,
	).Scan(&approvalStepID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.logger.Error("approve step update failed", "run_id", runID, "error", err)
		return err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		r.logger.Info("approve idempotent", "run_id", runID)
		return tx.Commit(ctx)
	}

	approvalPayload, err := json.Marshal(map[string]domain.StepStatus{
		"status": domain.StepSuccess,
	})
	if err != nil {
		r.logger.Error("marshal approve payload failed", "run_id", runID, "error", err)
		return err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO events (id, run_id, step_id, type, payload)
		 VALUES ($1, $2, $3, $4, $5::jsonb)`,
		uuid.New(),
		runID,
		approvalStepID,
		"STEP_APPROVED",
		approvalPayload,
	)
	if err != nil {
		r.logger.Error("insert step approved event failed", "run_id", runID, "error", err)
		return err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO events (id, run_id, type, payload)
		 VALUES ($1, $2, $3, $4)`,
		uuid.New(), runID, "RUN_APPROVED", `{"approved_by":"user"}`,
	)
	if err != nil {
		r.logger.Error("insert approve event failed", "run_id", runID, "error", err)
		return err
	}

	var remaining int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM steps
		WHERE run_id=$1 AND status <> $2
	`, runID, domain.StepSuccess).Scan(&remaining); err != nil {
		r.logger.Error("count remaining steps failed", "run_id", runID, "error", err)
		return err
	}

	newStatus := domain.RunRunning
	if remaining == 0 {
		newStatus = domain.RunSuccess
	}

	_, err = tx.Exec(ctx,
		`UPDATE runs SET status=$2, updated_at=NOW() WHERE id=$1`,
		runID, newStatus,
	)
	if err != nil {
		r.logger.Error("update run status failed", "run_id", runID, "error", err)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("commit approve failed", "run_id", runID, "error", err)
		return err
	}

	metrics.IncStepStatus(string(domain.StepSuccess))
	metrics.IncRunStatus(string(newStatus))
	r.logger.Info("run approved",
		"run_id", runID,
		"new_status", newStatus,
	)

	return nil
}
