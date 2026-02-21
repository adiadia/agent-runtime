package executors

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type LLMExecutor struct{}

func (e *LLMExecutor) Execute(
	ctx context.Context,
	runID uuid.UUID,
) (json.RawMessage, error) {

	time.Sleep(2 * time.Second)

	return json.RawMessage(`{
		"type":"llm",
		"text":"hello from llm step"
	}`), nil
}
