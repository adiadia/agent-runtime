// SPDX-License-Identifier: Apache-2.0

package httptransport

import (
	"context"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
)

type RunCreator interface {
	CreateRun(ctx context.Context, params domain.CreateRunParams) (uuid.UUID, error)
	GetRun(ctx context.Context, id uuid.UUID) (domain.RunStatus, error)
	GetRunCost(ctx context.Context, id uuid.UUID) (domain.RunCostBreakdown, error)
	CancelRun(ctx context.Context, id uuid.UUID) error
	ApproveRun(ctx context.Context, id uuid.UUID) error
}

type StepLister interface {
	ListSteps(ctx context.Context, runID uuid.UUID) ([]domain.StepRecord, error)
}

type APIKeyResolver interface {
	ResolveAPIKey(ctx context.Context, bearerToken string) (auth.APIKey, bool, error)
}

type APIKeyManager interface {
	CreateAPIKey(ctx context.Context, params domain.CreateAPIKeyParams) (domain.CreatedAPIKey, error)
	ListAPIKeys(ctx context.Context) ([]domain.APIKeyRecord, error)
	RevokeAPIKey(ctx context.Context, id uuid.UUID) error
}

type EventStreamer interface {
	ListEventsAfter(ctx context.Context, runID uuid.UUID, afterSeq int64) ([]domain.EventRecord, error)
	ResolveCursorByEventID(ctx context.Context, runID uuid.UUID, eventID uuid.UUID) (int64, error)
}

type HealthChecker interface {
	Check(ctx context.Context) error
}
