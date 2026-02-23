// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
)

const (
	webhookRetryAttempts = 3
	webhookRetryBase     = 300 * time.Millisecond
	webhookHeaderSig     = "X-Signature"
)

type terminalWebhookPayload struct {
	RunID      uuid.UUID        `json:"run_id"`
	Status     domain.RunStatus `json:"status"`
	FinishedAt time.Time        `json:"finished_at"`
}

func (w *Worker) deliverTerminalWebhook(
	ctx context.Context,
	runID uuid.UUID,
	status domain.RunStatus,
	finishedAt time.Time,
	webhookURL string,
	webhookSecret string,
) {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" || w.httpClient == nil {
		return
	}

	body, err := json.Marshal(terminalWebhookPayload{
		RunID:      runID,
		Status:     status,
		FinishedAt: finishedAt,
	})
	if err != nil {
		w.logger.Error("webhook payload marshal failed",
			"run_id", runID,
			"status", status,
			"error", err,
		)
		return
	}

	signature := signWebhookPayload(webhookSecret, body)

	var lastErr error
	for attempt := 1; attempt <= webhookRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			w.logger.Error("webhook request build failed",
				"run_id", runID,
				"status", status,
				"attempt", attempt,
				"error", err,
			)
			break
		}
		req.Header.Set("Content-Type", "application/json")
		if signature != "" {
			req.Header.Set(webhookHeaderSig, signature)
		}

		resp, err := w.httpClient.Do(req)
		if err != nil {
			lastErr = err
			w.logger.Warn("webhook failure",
				"run_id", runID,
				"status", status,
				"attempt", attempt,
				"error", err,
			)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
				w.logger.Info("webhook success",
					"run_id", runID,
					"status", status,
					"attempt", attempt,
					"response_status", resp.StatusCode,
				)
				return
			}

			lastErr = fmt.Errorf("non-2xx response: %d", resp.StatusCode)
			w.logger.Warn("webhook failure",
				"run_id", runID,
				"status", status,
				"attempt", attempt,
				"response_status", resp.StatusCode,
			)
		}

		if attempt < webhookRetryAttempts {
			wait := webhookRetryBase * time.Duration(1<<(attempt-1))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				w.logger.Warn("webhook canceled before retry",
					"run_id", runID,
					"status", status,
					"attempt", attempt,
					"error", ctx.Err(),
				)
				return
			case <-timer.C:
			}
		}
	}

	if lastErr != nil {
		w.logger.Error("webhook retries exhausted",
			"run_id", runID,
			"status", status,
			"error", lastErr,
		)
	}
}

func signWebhookPayload(secret string, payload []byte) string {
	if strings.TrimSpace(secret) == "" {
		return ""
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
