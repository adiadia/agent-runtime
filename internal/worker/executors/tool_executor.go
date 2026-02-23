// SPDX-License-Identifier: Apache-2.0

package executors

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ToolExecutor struct{}

func (e *ToolExecutor) Execute(
	ctx context.Context,
	runID uuid.UUID,
) (json.RawMessage, float64, error) {

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-timer.C:
	}

	return json.RawMessage(`{
		"type":"tool",
		"text":"mock tool ok"
	}`), 0, nil
}
