// SPDX-License-Identifier: Apache-2.0

package executors

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestLLMExecutorExecute(t *testing.T) {
	t.Parallel()

	exec := &LLMExecutor{}
	out, cost, err := exec.Execute(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Fatalf("expected llm execution to return positive cost, got %f", cost)
	}

	var payload map[string]string
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("expected valid json output, got %v", err)
	}
	if payload["type"] != "llm" {
		t.Fatalf("expected type=llm got %s", payload["type"])
	}
}

func TestToolExecutorExecute(t *testing.T) {
	t.Parallel()

	exec := &ToolExecutor{}
	out, cost, err := exec.Execute(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost < 0 {
		t.Fatalf("expected non-negative tool cost, got %f", cost)
	}

	var payload map[string]string
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("expected valid json output, got %v", err)
	}
	if payload["type"] != "tool" {
		t.Fatalf("expected type=tool got %s", payload["type"])
	}
}
