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
) (json.RawMessage, error) {

	time.Sleep(2 * time.Second)

	return json.RawMessage(`{
		"type":"tool",
		"text":"mock tool ok"
	}`), nil
}
