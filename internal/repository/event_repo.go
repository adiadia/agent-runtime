// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"log/slog"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewEventRepository(pool *pgxpool.Pool, logger *slog.Logger) *EventRepository {
	if logger == nil {
		logger = slog.Default()
	}

	return &EventRepository{
		pool:   pool,
		logger: logger,
	}
}

func (r *EventRepository) ListEventsAfter(ctx context.Context, runID uuid.UUID, afterSeq int64) ([]domain.EventRecord, error) {
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("list events denied: missing api key id", "run_id", runID, "error", err)
		return nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT e.id, e.seq, e.run_id, e.type, e.payload, e.created_at
		FROM events e
		JOIN runs r ON e.run_id = r.id
		WHERE e.run_id=$1
		  AND r.api_key_id=$2
		  AND e.seq > $3
		ORDER BY e.seq ASC
	`,
		runID,
		apiKeyID,
		afterSeq,
	)
	if err != nil {
		r.logger.Error("list events query failed",
			"run_id", runID,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.EventRecord, 0, 8)
	for rows.Next() {
		var ev domain.EventRecord
		if err := rows.Scan(
			&ev.ID,
			&ev.Seq,
			&ev.RunID,
			&ev.Type,
			&ev.Payload,
			&ev.CreatedAt,
		); err != nil {
			r.logger.Error("scan event row failed",
				"run_id", runID,
				"api_key_id", apiKeyID,
				"error", err,
			)
			return nil, err
		}
		out = append(out, ev)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("events rows iteration failed",
			"run_id", runID,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return nil, err
	}

	return out, nil
}

func (r *EventRepository) ResolveCursorByEventID(ctx context.Context, runID uuid.UUID, eventID uuid.UUID) (int64, error) {
	apiKeyID, err := apiKeyIDFromContext(ctx)
	if err != nil {
		r.logger.Warn("resolve cursor denied: missing api key id", "run_id", runID, "error", err)
		return 0, err
	}

	var seq int64
	if err := r.pool.QueryRow(ctx, `
		SELECT e.seq
		FROM events e
		JOIN runs r ON e.run_id = r.id
		WHERE e.id=$1
		  AND e.run_id=$2
		  AND r.api_key_id=$3
	`,
		eventID,
		runID,
		apiKeyID,
	).Scan(&seq); err != nil {
		r.logger.Error("resolve event cursor failed",
			"run_id", runID,
			"event_id", eventID,
			"api_key_id", apiKeyID,
			"error", err,
		)
		return 0, err
	}

	return seq, nil
}
