// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

type StepExecutor interface {
	Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error)
}
