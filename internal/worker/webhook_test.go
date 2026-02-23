// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
)

func TestDeliverTerminalWebhookRetriesAndSigns(t *testing.T) {
	var attempts int32
	runID := uuid.New()
	finishedAt := time.Now().UTC().Truncate(time.Second)
	secret := "super-secret"

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		current := atomic.AddInt32(&attempts, 1)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		gotSig := r.Header.Get(webhookHeaderSig)
		wantSig := signWebhookPayload(secret, body)
		if gotSig != wantSig {
			t.Fatalf("expected signature %q got %q", wantSig, gotSig)
		}

		var payload terminalWebhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.RunID != runID {
			t.Fatalf("expected run id %s got %s", runID, payload.RunID)
		}
		if payload.Status != domain.RunFailed {
			t.Fatalf("expected status %s got %s", domain.RunFailed, payload.Status)
		}
		if !payload.FinishedAt.Equal(finishedAt) {
			t.Fatalf("expected finished_at %s got %s", finishedAt, payload.FinishedAt)
		}

		if current < 3 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("fail")),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}

	w := &Worker{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpClient: client,
	}

	w.deliverTerminalWebhook(context.Background(), runID, domain.RunFailed, finishedAt, "http://webhook.local/callback", secret)

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 webhook attempts got %d", got)
	}
}

func TestDeliverTerminalWebhookStopsAfterRetryLimit(t *testing.T) {
	var attempts int32
	runID := uuid.New()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("fail")),
			Header:     make(http.Header),
		}, nil
	})}

	w := &Worker{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpClient: client,
	}

	w.deliverTerminalWebhook(context.Background(), runID, domain.RunSuccess, time.Now().UTC(), "http://webhook.local/callback", "")

	if got := atomic.LoadInt32(&attempts); got != webhookRetryAttempts {
		t.Fatalf("expected %d attempts got %d", webhookRetryAttempts, got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
