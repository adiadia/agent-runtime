// SPDX-License-Identifier: Apache-2.0

package executors

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type LLMExecutor struct{}

const (
	llmModelPricePerToken = 0.000002
	llmPromptTokens       = 180
	llmCompletionTokens   = 72
)

func (e *LLMExecutor) Execute(
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

	totalTokens := llmPromptTokens + llmCompletionTokens
	costUSD := float64(totalTokens) * llmModelPricePerToken

	return json.RawMessage(`{
		"type":"llm",
		"text":"hello from llm step"
	}`), costUSD, nil
}
