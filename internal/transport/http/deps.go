package httptransport

import (
	"context"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
)

type RunCreator interface {
	CreateRun(ctx context.Context) (uuid.UUID, error)
	GetRun(ctx context.Context, id uuid.UUID) (domain.RunStatus, error)
	CancelRun(ctx context.Context, id uuid.UUID) error
	ApproveRun(ctx context.Context, id uuid.UUID) error
}

type StepLister interface {
	ListSteps(ctx context.Context, runID uuid.UUID) ([]domain.StepRecord, error)
}
