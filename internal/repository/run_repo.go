package repository

import (
	"context"
	"log/slog"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewRunRepository(pool *pgxpool.Pool, logger *slog.Logger) *RunRepository {
	return &RunRepository{
		pool:   pool,
		logger: logger,
	}
}

func (r *RunRepository) CreateRun(ctx context.Context) (uuid.UUID, error) {
	runID := uuid.New()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO runs (id, status) VALUES ($1, $2)`,
		runID, domain.RunPending,
	)
	if err != nil {
		r.logger.Error("insert run failed", "run_id", runID, "error", err)
		return uuid.Nil, err
	}

	steps := []domain.StepName{
		domain.StepLLM,
		domain.StepTool,
		domain.StepApproval,
	}

	for _, name := range steps {
		if _, err := tx.Exec(ctx,
			`INSERT INTO steps (id, run_id, name, status)
			 VALUES ($1, $2, $3, $4)`,
			uuid.New(),
			runID,
			name,
			domain.StepPending,
		); err != nil {
			r.logger.Error("insert step failed",
				"run_id", runID,
				"step", name,
				"error", err,
			)
			return uuid.Nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("commit failed", "run_id", runID, "error", err)
		return uuid.Nil, err
	}

	r.logger.Info("run created", "run_id", runID)
	return runID, nil
}
func (r *RunRepository) GetRun(ctx context.Context, id uuid.UUID) (domain.RunStatus, error) {
	var status domain.RunStatus

	err := r.pool.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1`,
		id,
	).Scan(&status)

	if err != nil {
		r.logger.Error("get run failed", "run_id", id, "error", err)
		return "", err
	}

	return status, nil
}

func (r *RunRepository) CancelRun(ctx context.Context, runID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return err
	}
	defer tx.Rollback(ctx)

	var status domain.RunStatus
	if err := tx.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1`,
		runID,
	).Scan(&status); err != nil {
		r.logger.Error("read run status failed", "run_id", runID, "error", err)
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

	r.logger.Info("run canceled", "run_id", runID)
	return nil
}

func (r *RunRepository) ApproveRun(ctx context.Context, runID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.logger.Error("begin tx failed", "error", err)
		return err
	}
	defer tx.Rollback(ctx)

	var runStatus domain.RunStatus
	if err := tx.QueryRow(ctx,
		`SELECT status FROM runs WHERE id=$1`,
		runID,
	).Scan(&runStatus); err != nil {
		r.logger.Error("read run status failed", "run_id", runID, "error", err)
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

	cmd, err := tx.Exec(ctx, `
		UPDATE steps
		SET status=$2,
		    started_at=COALESCE(started_at, NOW()),
		    finished_at=COALESCE(finished_at, NOW())
		WHERE run_id=$1
		  AND name='APPROVAL'
		  AND status=$3
	`,
		runID,
		domain.StepSuccess,
		domain.StepWaiting,
	)
	if err != nil {
		r.logger.Error("approve step update failed", "run_id", runID, "error", err)
		return err
	}

	if cmd.RowsAffected() == 0 {
		r.logger.Info("approve idempotent", "run_id", runID)
		return tx.Commit(ctx)
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

	r.logger.Info("run approved",
		"run_id", runID,
		"new_status", newStatus,
	)

	return nil
}
