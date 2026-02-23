// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
)

type fakeExecutor struct {
	output json.RawMessage
	cost   float64
	err    error
	called bool
	runID  uuid.UUID
}

func (f *fakeExecutor) Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error) {
	f.called = true
	f.runID = runID
	return f.output, f.cost, f.err
}

func TestNewDefaults(t *testing.T) {
	w := New(Deps{})

	if w.logger == nil {
		t.Fatal("expected default logger to be set")
	}
	if w.reclaimAfter != 5*time.Minute {
		t.Fatalf("expected default reclaimAfter=5m, got %s", w.reclaimAfter)
	}
	if w.maxAttempts != 3 {
		t.Fatalf("expected default maxAttempts=3, got %d", w.maxAttempts)
	}
	if w.retryBaseDelay != 2*time.Second {
		t.Fatalf("expected default retryBaseDelay=2s, got %s", w.retryBaseDelay)
	}
	if w.defaultStepTimeout != 30*time.Second {
		t.Fatalf("expected default defaultStepTimeout=30s, got %s", w.defaultStepTimeout)
	}
	if w.apiKeyID != uuid.Nil {
		t.Fatalf("expected default apiKeyID to be nil UUID, got %s", w.apiKeyID)
	}

	if _, ok := w.executors[domain.StepLLM]; !ok {
		t.Fatal("expected LLM executor to be registered")
	}
	if _, ok := w.executors[domain.StepTool]; !ok {
		t.Fatal("expected Tool executor to be registered")
	}
}

func TestNewCustomValues(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKeyID := uuid.New()

	w := New(Deps{
		Logger:             logger,
		ReclaimAfter:       30 * time.Second,
		MaxAttempts:        7,
		RetryBaseDelay:     9 * time.Second,
		DefaultStepTimeout: 11 * time.Second,
		APIKeyID:           apiKeyID,
	})

	if w.logger != logger {
		t.Fatal("expected provided logger to be used")
	}
	if w.reclaimAfter != 30*time.Second {
		t.Fatalf("expected reclaimAfter=30s, got %s", w.reclaimAfter)
	}
	if w.maxAttempts != 7 {
		t.Fatalf("expected maxAttempts=7, got %d", w.maxAttempts)
	}
	if w.retryBaseDelay != 9*time.Second {
		t.Fatalf("expected retryBaseDelay=9s, got %s", w.retryBaseDelay)
	}
	if w.defaultStepTimeout != 11*time.Second {
		t.Fatalf("expected defaultStepTimeout=11s, got %s", w.defaultStepTimeout)
	}
	if w.apiKeyID != apiKeyID {
		t.Fatalf("expected apiKeyID=%s, got %s", apiKeyID, w.apiKeyID)
	}
}

func TestExecuteStepSuccess(t *testing.T) {
	runID := uuid.New()
	want := json.RawMessage(`{"ok":true}`)
	exec := &fakeExecutor{output: want}

	w := &Worker{
		executors: map[domain.StepName]StepExecutor{
			domain.StepLLM: exec,
		},
	}

	out, cost, err := w.executeStep(context.Background(), claimedStep{
		RunID: runID,
		Name:  domain.StepLLM,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !exec.called {
		t.Fatal("expected executor to be called")
	}
	if exec.runID != runID {
		t.Fatalf("expected run id %s got %s", runID, exec.runID)
	}
	if string(out) != string(want) {
		t.Fatalf("expected output %s got %s", string(want), string(out))
	}
	if cost != 0 {
		t.Fatalf("expected cost 0 got %f", cost)
	}
}

func TestExecuteStepError(t *testing.T) {
	wantErr := errors.New("boom")
	exec := &fakeExecutor{err: wantErr}

	w := &Worker{
		executors: map[domain.StepName]StepExecutor{
			domain.StepTool: exec,
		},
	}

	_, _, err := w.executeStep(context.Background(), claimedStep{
		RunID: uuid.New(),
		Name:  domain.StepTool,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v got %v", wantErr, err)
	}
}

type blockingExecutor struct{}

func (b *blockingExecutor) Execute(ctx context.Context, runID uuid.UUID) (json.RawMessage, float64, error) {
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

func TestExecuteStepTimeout(t *testing.T) {
	w := &Worker{
		executors: map[domain.StepName]StepExecutor{
			domain.StepLLM: &blockingExecutor{},
		},
	}

	_, _, err := w.executeStep(context.Background(), claimedStep{
		RunID:   uuid.New(),
		Name:    domain.StepLLM,
		Timeout: 20 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestExecuteStepMissingExecutor(t *testing.T) {
	w := &Worker{
		executors: map[domain.StepName]StepExecutor{},
	}

	_, _, err := w.executeStep(context.Background(), claimedStep{
		RunID: uuid.New(),
		Name:  domain.StepApproval,
	})
	if err == nil {
		t.Fatal("expected missing executor error")
	}
	if !strings.Contains(err.Error(), "no executor registered") {
		t.Fatalf("unexpected error: %v", err)
	}
}
