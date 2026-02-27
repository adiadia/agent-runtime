// SPDX-License-Identifier: Apache-2.0

package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRouter_CreateRun(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{createRunID: runID}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["run_id"] != runID.String() {
		t.Fatalf("expected run_id %s got %s", runID, resp["run_id"])
	}

	if !runRepo.createCalled {
		t.Fatalf("expected CreateRun to be called")
	}
}

func TestRouter_CreateRunError(t *testing.T) {
	runRepo := &mockRunRepo{createErr: errors.New("insert failed")}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 got %d", rec.Code)
	}
}

func TestRouter_CreateRunIdempotencyKey(t *testing.T) {
	runRepo := &mockRunRepo{}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req1 := httptest.NewRequest(http.MethodPost, "/runs", nil)
	req1.Header.Set(headerIdempotencyKey, "same-key")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first status 200 got %d", rec1.Code)
	}

	var resp1 map[string]string
	if err := json.NewDecoder(rec1.Body).Decode(&resp1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/runs", nil)
	req2.Header.Set(headerIdempotencyKey, "same-key")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected second status 200 got %d", rec2.Code)
	}

	var resp2 map[string]string
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}

	if resp1["run_id"] != resp2["run_id"] {
		t.Fatalf("expected same run_id for same idempotency key, got %s and %s", resp1["run_id"], resp2["run_id"])
	}

	if runRepo.createCalls != 2 {
		t.Fatalf("expected CreateRun called twice got %d", runRepo.createCalls)
	}
}

func TestRouter_CreateRunConcurrentLimitExceeded(t *testing.T) {
	runRepo := &mockRunRepo{createErr: domain.ErrMaxConcurrentRunsExceeded}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header to be set")
	}
}

func TestRouter_CreateRunWithWebhookURL(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{createRunID: runID}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"webhook_url":"https://example.com/webhook"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if runRepo.createParams.WebhookURL != "https://example.com/webhook" {
		t.Fatalf("expected webhook_url to be forwarded, got %q", runRepo.createParams.WebhookURL)
	}
}

func TestRouter_CreateRunWithPriorityAndTemplateName(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{createRunID: runID}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/runs",
		bytes.NewBufferString(`{"webhook_url":"https://example.com/webhook","priority":7,"template_name":"ops-template"}`),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if runRepo.createParams.Priority != 7 {
		t.Fatalf("expected priority to be forwarded, got %d", runRepo.createParams.Priority)
	}
	if runRepo.createParams.TemplateName != "ops-template" {
		t.Fatalf("expected template_name to be forwarded, got %q", runRepo.createParams.TemplateName)
	}
}

func TestRouter_CreateRunRejectsStringPriority(t *testing.T) {
	runRepo := &mockRunRepo{createRunID: uuid.New()}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"priority":"normal"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
	if runRepo.createCalled {
		t.Fatal("expected CreateRun not to be called for invalid priority type")
	}
}

func TestRouter_CreateRunRejectsNonIntegerPriority(t *testing.T) {
	runRepo := &mockRunRepo{createRunID: uuid.New()}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"priority":10.5}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
	if runRepo.createCalled {
		t.Fatal("expected CreateRun not to be called for invalid priority type")
	}
}

func TestRouter_CreateRunRejectsInvalidWebhookURL(t *testing.T) {
	runRepo := &mockRunRepo{createRunID: uuid.New()}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"webhook_url":"file:///tmp/hook"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
}

func TestRouter_CreateRunTemplateNotFound(t *testing.T) {
	runRepo := &mockRunRepo{createErr: domain.ErrWorkflowTemplateNotFound}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"template_name":"does-not-exist"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
}

func TestRouter_CreateAPIKeyRequiresAdminToken(t *testing.T) {
	apiKeyAdmin := &mockAPIKeyManager{}
	router := NewRouter(Deps{
		RunRepo:     &mockRunRepo{},
		StepRepo:    &mockStepLister{},
		APIKeyAdmin: apiKeyAdmin,
		AdminToken:  "master-token",
		Logger:      discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api-keys", bytes.NewBufferString(`{"name":"my-key"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api-keys", bytes.NewBufferString(`{"name":"my-key"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 got %d", rec.Code)
	}
}

func TestRouter_CreateAPIKey(t *testing.T) {
	apiKeyID := uuid.New()
	apiKeyAdmin := &mockAPIKeyManager{
		createResp: domain.CreatedAPIKey{
			ID:    apiKeyID,
			Token: "sk_live_abc123",
		},
	}
	router := NewRouter(Deps{
		RunRepo:     &mockRunRepo{},
		StepRepo:    &mockStepLister{},
		APIKeyAdmin: apiKeyAdmin,
		AdminToken:  "master-token",
		Logger:      discardLogger(),
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/api-keys",
		bytes.NewBufferString(`{"name":"my-key","max_concurrent_runs":5,"max_requests_per_min":60}`),
	)
	req.Header.Set("Authorization", "Bearer master-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if apiKeyAdmin.createParams.Name != "my-key" {
		t.Fatalf("expected name to be forwarded, got %q", apiKeyAdmin.createParams.Name)
	}
	if apiKeyAdmin.createParams.MaxConcurrentRuns != 5 {
		t.Fatalf("expected max_concurrent_runs 5 got %d", apiKeyAdmin.createParams.MaxConcurrentRuns)
	}
	if apiKeyAdmin.createParams.MaxRequestsPerMin != 60 {
		t.Fatalf("expected max_requests_per_min 60 got %d", apiKeyAdmin.createParams.MaxRequestsPerMin)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["api_key_id"] != apiKeyID.String() {
		t.Fatalf("expected api_key_id %s got %s", apiKeyID, resp["api_key_id"])
	}
	if resp["token"] != "sk_live_abc123" {
		t.Fatalf("expected token to be returned once, got %s", resp["token"])
	}
}

func TestRouter_ListAPIKeys(t *testing.T) {
	apiKeyAdmin := &mockAPIKeyManager{
		listResp: []domain.APIKeyRecord{
			{
				ID:                uuid.New(),
				Name:              "key-a",
				MaxConcurrentRuns: 5,
				MaxRequestsPerMin: 60,
				CreatedAt:         time.Now().UTC(),
			},
		},
	}
	router := NewRouter(Deps{
		RunRepo:     &mockRunRepo{},
		StepRepo:    &mockStepLister{},
		APIKeyAdmin: apiKeyAdmin,
		AdminToken:  "master-token",
		Logger:      discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api-keys", nil)
	req.Header.Set("Authorization", "Bearer master-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if !apiKeyAdmin.listCalled {
		t.Fatalf("expected ListAPIKeys to be called")
	}

	var resp struct {
		APIKeys []domain.APIKeyRecord `json:"api_keys"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.APIKeys) != 1 {
		t.Fatalf("expected 1 api key got %d", len(resp.APIKeys))
	}
}

func TestRouter_DeleteAPIKey(t *testing.T) {
	apiKeyAdmin := &mockAPIKeyManager{}
	router := NewRouter(Deps{
		RunRepo:     &mockRunRepo{},
		StepRepo:    &mockStepLister{},
		APIKeyAdmin: apiKeyAdmin,
		AdminToken:  "master-token",
		Logger:      discardLogger(),
	})

	apiKeyID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api-keys/"+apiKeyID.String(), nil)
	req.Header.Set("Authorization", "Bearer master-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 got %d", rec.Code)
	}
	if apiKeyAdmin.revokeID != apiKeyID {
		t.Fatalf("expected revoke id %s got %s", apiKeyID, apiKeyAdmin.revokeID)
	}
}

func TestRouter_HealthzUnauthenticated(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:        &mockRunRepo{},
		StepRepo:       &mockStepLister{},
		Logger:         discardLogger(),
		APIKeyResolver: &mockAPIKeyResolver{},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if got := rec.Header().Get(headerRequestID); got == "" {
		t.Fatalf("expected %s response header to be set", headerRequestID)
	}
}

func TestRouter_HealthzPreservesRequestID(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(headerRequestID, "req-from-client")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if got := rec.Header().Get(headerRequestID); got != "req-from-client" {
		t.Fatalf("expected %s req-from-client got %q", headerRequestID, got)
	}
}

func TestRouter_HealthzNotReadyWhenSchemaCheckFails(t *testing.T) {
	healthChecker := &mockHealthChecker{err: errors.New("schema missing")}
	router := NewRouter(Deps{
		RunRepo:       &mockRunRepo{},
		StepRepo:      &mockStepLister{},
		Logger:        discardLogger(),
		HealthChecker: healthChecker,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 got %d", rec.Code)
	}
	if healthChecker.calls != 1 {
		t.Fatalf("expected health checker call count 1 got %d", healthChecker.calls)
	}
}

func TestRouter_MetricsUnauthenticated(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:        &mockRunRepo{},
		StepRepo:       &mockStepLister{},
		Logger:         discardLogger(),
		APIKeyResolver: &mockAPIKeyResolver{},
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "runs_total") {
		t.Fatalf("expected prometheus output to include runs_total metric, got %q", rec.Body.String())
	}
}

func TestRouter_VersionUnauthenticated(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:        &mockRunRepo{},
		StepRepo:       &mockStepLister{},
		Logger:         discardLogger(),
		APIKeyResolver: &mockAPIKeyResolver{},
		Version:        "1.2.3",
		Commit:         "abc123",
		BuildDate:      "2026-02-23T00:00:00Z",
	})

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["version"] != "1.2.3" {
		t.Fatalf("expected version 1.2.3 got %q", resp["version"])
	}
	if resp["commit"] != "abc123" {
		t.Fatalf("expected commit abc123 got %q", resp["commit"])
	}
	if resp["build_date"] != "2026-02-23T00:00:00Z" {
		t.Fatalf("expected build_date 2026-02-23T00:00:00Z got %q", resp["build_date"])
	}
}

func TestRouter_GetRunNotFound(t *testing.T) {
	runRepo := &mockRunRepo{getRunErr: pgx.ErrNoRows}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}

	if runRepo.getRunID == uuid.Nil {
		t.Fatalf("expected GetRun to be called")
	}
}

func TestRouter_GetRunError(t *testing.T) {
	runRepo := &mockRunRepo{getRunErr: errors.New("db failed")}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 got %d", rec.Code)
	}
}

func TestRouter_GetRunSuccess(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{getRunStatus: domain.RunRunning}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["id"] != runID.String() {
		t.Fatalf("expected id %s got %s", runID, resp["id"])
	}

	if resp["status"] != string(domain.RunRunning) {
		t.Fatalf("expected status %s got %s", domain.RunRunning, resp["status"])
	}
}

func TestRouter_GetRunInvalidID(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
}

func TestRouter_GetRunCost(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{
		getRunCost: domain.RunCostBreakdown{
			RunID:        runID,
			TotalCostUSD: 1.2345,
			Steps: []domain.StepCostBreakdown{
				{ID: uuid.New(), Name: string(domain.StepLLM), Status: string(domain.StepSuccess), CostUSD: 1.2345},
			},
		},
	}

	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/cost", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp domain.RunCostBreakdown
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RunID != runID {
		t.Fatalf("expected run_id %s got %s", runID, resp.RunID)
	}
	if resp.TotalCostUSD != 1.2345 {
		t.Fatalf("expected total_cost_usd 1.2345 got %f", resp.TotalCostUSD)
	}
	if len(resp.Steps) != 1 {
		t.Fatalf("expected 1 step cost entry got %d", len(resp.Steps))
	}
}

func TestRouter_GetRunCostNotFound(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{getRunCostErr: pgx.ErrNoRows}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/cost", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}
}

func TestRouter_ListSteps(t *testing.T) {
	runID := uuid.New()
	steps := []domain.StepRecord{
		{ID: uuid.New(), Name: "demo", Status: string(domain.StepPending)},
	}

	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{steps: steps},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/steps", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}

	var resp struct {
		RunID string              `json:"run_id"`
		Steps []domain.StepRecord `json:"steps"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.RunID != runID.String() {
		t.Fatalf("expected run id %s got %s", runID, resp.RunID)
	}

	if len(resp.Steps) != len(steps) {
		t.Fatalf("expected %d steps got %d", len(steps), len(resp.Steps))
	}
}

func TestRouter_ListStepsError(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{err: errors.New("query failed")},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+uuid.New().String()+"/steps", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 got %d", rec.Code)
	}
}

func TestRouter_ListStepsNotFound(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{err: pgx.ErrNoRows},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+uuid.New().String()+"/steps", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}
}

func TestRouter_ListStepsInvalidID(t *testing.T) {
	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{},
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/not-a-uuid/steps", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
}

func TestRouter_StreamEvents(t *testing.T) {
	runID := uuid.New()
	ev := domain.EventRecord{
		ID:        uuid.New(),
		Seq:       1,
		RunID:     runID,
		Type:      "STEP_CLAIMED",
		Payload:   mustStatusPayload(t, domain.StepRunning),
		CreatedAt: time.Now().UTC(),
	}

	router := NewRouter(Deps{
		RunRepo:  &mockRunRepo{getRunStatus: domain.RunRunning},
		StepRepo: &mockStepLister{},
		EventRepo: &mockEventRepo{
			eventsByAfter: map[int64][]domain.EventRecord{
				0: []domain.EventRecord{ev},
			},
		},
		Logger: discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: step_update") {
		t.Fatalf("expected SSE event line, got body %q", body)
	}
	if !strings.Contains(body, ev.ID.String()) {
		t.Fatalf("expected SSE payload to include event id %s, got body %q", ev.ID, body)
	}
}

func TestRouter_StreamEventsInvalidSinceID(t *testing.T) {
	runID := uuid.New()
	router := NewRouter(Deps{
		RunRepo:   &mockRunRepo{getRunStatus: domain.RunRunning},
		StepRepo:  &mockStepLister{},
		EventRepo: &mockEventRepo{},
		Logger:    discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/events?since_id=not-valid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 got %d", rec.Code)
	}
}

func TestRouter_StreamEventsSinceEventID(t *testing.T) {
	runID := uuid.New()
	sinceEventID := uuid.New()
	ev := domain.EventRecord{
		ID:        uuid.New(),
		Seq:       6,
		RunID:     runID,
		Type:      "STEP_SUCCEEDED",
		Payload:   mustStatusPayload(t, domain.StepSuccess),
		CreatedAt: time.Now().UTC(),
	}

	eventRepo := &mockEventRepo{
		resolveCursorByEventID: map[uuid.UUID]int64{
			sinceEventID: 5,
		},
		eventsByAfter: map[int64][]domain.EventRecord{
			5: []domain.EventRecord{ev},
		},
	}

	router := NewRouter(Deps{
		RunRepo:   &mockRunRepo{getRunStatus: domain.RunRunning},
		StepRepo:  &mockStepLister{},
		EventRepo: eventRepo,
		Logger:    discardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(
		http.MethodGet,
		"/runs/"+runID.String()+"/events?since_id="+sinceEventID.String(),
		nil,
	).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if eventRepo.resolveEventID != sinceEventID {
		t.Fatalf("expected resolve cursor lookup for event id %s got %s", sinceEventID, eventRepo.resolveEventID)
	}
}

func TestRouter_StreamEventsRunNotFound(t *testing.T) {
	runID := uuid.New()
	router := NewRouter(Deps{
		RunRepo:   &mockRunRepo{getRunErr: pgx.ErrNoRows},
		StepRepo:  &mockStepLister{},
		EventRepo: &mockEventRepo{},
		Logger:    discardLogger(),
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID.String()+"/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}
}

func TestRouter_AuthEnforcedWhenResolverPresent(t *testing.T) {
	apiKeyID := uuid.New()
	runRepo := &mockRunRepo{createRunID: uuid.New()}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
		APIKeyResolver: &mockAPIKeyResolver{
			keyByToken: map[string]auth.APIKey{
				"secret": {
					ID:                apiKeyID,
					MaxConcurrentRuns: 5,
					MaxRequestsPerMin: 60,
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/runs", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 got %d", rec.Code)
	}

	authReq := httptest.NewRequest(http.MethodPost, "/runs", nil)
	authReq.Header.Set("Authorization", "Bearer secret")
	authRec := httptest.NewRecorder()

	router.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", authRec.Code)
	}
	gotAPIKeyID, ok := auth.APIKeyIDFromContext(runRepo.createCtx)
	if !ok {
		t.Fatal("expected api_key_id to be attached to context")
	}
	if gotAPIKeyID != apiKeyID {
		t.Fatalf("expected api_key_id %s got %s", apiKeyID, gotAPIKeyID)
	}
}

func TestRouter_CancelAndApprove(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	cancelReq := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/cancel", nil)
	cancelRec := httptest.NewRecorder()
	router.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel expected 200 got %d", cancelRec.Code)
	}
	if runRepo.cancelRunID != runID {
		t.Fatalf("expected cancel run id %s got %s", runID, runRepo.cancelRunID)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/approve", bytes.NewBufferString("{}"))
	approveRec := httptest.NewRecorder()
	router.ServeHTTP(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve expected 200 got %d", approveRec.Code)
	}
	if runRepo.approveRunID != runID {
		t.Fatalf("expected approve run id %s got %s", runID, runRepo.approveRunID)
	}
}

func TestRouter_CancelError(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{cancelErr: errors.New("update failed")}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 got %d", rec.Code)
	}
}

func TestRouter_CancelNotFound(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{cancelErr: pgx.ErrNoRows}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}
}

func TestRouter_ApproveError(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{approveErr: errors.New("update failed")}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 got %d", rec.Code)
	}
}

func TestRouter_ApproveNotFound(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{approveErr: pgx.ErrNoRows}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 got %d", rec.Code)
	}
}

func TestRouter_ApproveRequiresWaitingApproval(t *testing.T) {
	runID := uuid.New()
	runRepo := &mockRunRepo{approveErr: domain.ErrRunNotWaitingApproval}
	router := NewRouter(Deps{
		RunRepo:  runRepo,
		StepRepo: &mockStepLister{},
		Logger:   discardLogger(),
	})

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 got %d", rec.Code)
	}
}

func TestWriteJSONSetsHeadersAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"ok": "true"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content-type application/json got %s", got)
	}

	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["ok"] != "true" {
		t.Fatalf("expected ok=true got %s", payload["ok"])
	}
}

type mockRunRepo struct {
	createRunID   uuid.UUID
	createErr     error
	createCalled  bool
	createCalls   int
	createCtx     context.Context
	createParams  domain.CreateRunParams
	runByKey      map[string]uuid.UUID
	getRunStatus  domain.RunStatus
	getRunErr     error
	getRunID      uuid.UUID
	getRunCost    domain.RunCostBreakdown
	getRunCostErr error
	cancelErr     error
	cancelRunID   uuid.UUID
	approveErr    error
	approveRunID  uuid.UUID
}

func (m *mockRunRepo) CreateRun(ctx context.Context, params domain.CreateRunParams) (uuid.UUID, error) {
	m.createCalled = true
	m.createCalls++
	m.createCtx = ctx
	m.createParams = params

	if key, ok := auth.IdempotencyKeyFromContext(ctx); ok {
		if m.runByKey == nil {
			m.runByKey = make(map[string]uuid.UUID, 2)
		}
		if id, exists := m.runByKey[key]; exists {
			return id, m.createErr
		}
		id := m.createRunID
		if id == uuid.Nil {
			id = uuid.New()
		}
		m.runByKey[key] = id
		return id, m.createErr
	}

	if m.createRunID == uuid.Nil {
		m.createRunID = uuid.New()
	}
	return m.createRunID, m.createErr
}

func (m *mockRunRepo) GetRun(ctx context.Context, id uuid.UUID) (domain.RunStatus, error) {
	m.getRunID = id
	return m.getRunStatus, m.getRunErr
}

func (m *mockRunRepo) GetRunCost(ctx context.Context, id uuid.UUID) (domain.RunCostBreakdown, error) {
	m.getRunID = id
	return m.getRunCost, m.getRunCostErr
}

func (m *mockRunRepo) CancelRun(ctx context.Context, id uuid.UUID) error {
	m.cancelRunID = id
	return m.cancelErr
}

func (m *mockRunRepo) ApproveRun(ctx context.Context, id uuid.UUID) error {
	m.approveRunID = id
	return m.approveErr
}

type mockStepLister struct {
	steps []domain.StepRecord
	err   error
}

func (m *mockStepLister) ListSteps(ctx context.Context, runID uuid.UUID) ([]domain.StepRecord, error) {
	return m.steps, m.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustStatusPayload(t *testing.T, status domain.StepStatus) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]domain.StepStatus{"status": status})
	if err != nil {
		t.Fatalf("marshal status payload: %v", err)
	}
	return b
}

type mockAPIKeyResolver struct {
	keyByToken map[string]auth.APIKey
	err        error
}

func (m *mockAPIKeyResolver) ResolveAPIKey(ctx context.Context, bearerToken string) (auth.APIKey, bool, error) {
	if m.err != nil {
		return auth.APIKey{}, false, m.err
	}

	key, ok := m.keyByToken[bearerToken]
	return key, ok, nil
}

type mockAPIKeyManager struct {
	createResp   domain.CreatedAPIKey
	createErr    error
	createParams domain.CreateAPIKeyParams
	listResp     []domain.APIKeyRecord
	listErr      error
	listCalled   bool
	revokeID     uuid.UUID
	revokeErr    error
}

func (m *mockAPIKeyManager) CreateAPIKey(ctx context.Context, params domain.CreateAPIKeyParams) (domain.CreatedAPIKey, error) {
	m.createParams = params
	if m.createResp.ID == uuid.Nil && m.createErr == nil {
		m.createResp.ID = uuid.New()
		m.createResp.Token = "sk_live_generated"
	}
	return m.createResp, m.createErr
}

func (m *mockAPIKeyManager) ListAPIKeys(ctx context.Context) ([]domain.APIKeyRecord, error) {
	m.listCalled = true
	return m.listResp, m.listErr
}

func (m *mockAPIKeyManager) RevokeAPIKey(ctx context.Context, id uuid.UUID) error {
	m.revokeID = id
	return m.revokeErr
}

type mockEventRepo struct {
	eventsByAfter          map[int64][]domain.EventRecord
	listErr                error
	listCalls              int
	resolveCursorByEventID map[uuid.UUID]int64
	resolveErr             error
	resolveEventID         uuid.UUID
}

func (m *mockEventRepo) ListEventsAfter(ctx context.Context, runID uuid.UUID, afterSeq int64) ([]domain.EventRecord, error) {
	m.listCalls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.eventsByAfter == nil {
		return nil, nil
	}
	return m.eventsByAfter[afterSeq], nil
}

func (m *mockEventRepo) ResolveCursorByEventID(ctx context.Context, runID uuid.UUID, eventID uuid.UUID) (int64, error) {
	m.resolveEventID = eventID
	if m.resolveErr != nil {
		return 0, m.resolveErr
	}
	if m.resolveCursorByEventID == nil {
		return 0, pgx.ErrNoRows
	}
	seq, ok := m.resolveCursorByEventID[eventID]
	if !ok {
		return 0, pgx.ErrNoRows
	}
	return seq, nil
}

type mockHealthChecker struct {
	err   error
	calls int
}

func (m *mockHealthChecker) Check(ctx context.Context) error {
	m.calls++
	return m.err
}
