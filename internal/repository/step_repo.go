package repository

import (
	"context"
	"log/slog"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StepRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStepRepository(pool *pgxpool.Pool, logger *slog.Logger) *StepRepository {
	return &StepRepository{
		pool:   pool,
		logger: logger,
	}
}

func (s *StepRepository) ListSteps(ctx context.Context, runID uuid.UUID) ([]domain.StepRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, status
		FROM steps
		WHERE run_id=$1
		ORDER BY created_at ASC
	`, runID)
	if err != nil {
		s.logger.Error("list steps query failed",
			"run_id", runID,
			"error", err,
		)
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.StepRecord, 0, 4)

	for rows.Next() {
		var st domain.StepRecord
		if err := rows.Scan(&st.ID, &st.Name, &st.Status); err != nil {
			s.logger.Error("scan step row failed",
				"run_id", runID,
				"error", err,
			)
			return nil, err
		}
		out = append(out, st)
	}

	if err := rows.Err(); err != nil {
		s.logger.Error("rows iteration failed",
			"run_id", runID,
			"error", err,
		)
		return nil, err
	}

	s.logger.Debug("steps fetched",
		"run_id", runID,
		"count", len(out),
	)

	return out, nil
}
