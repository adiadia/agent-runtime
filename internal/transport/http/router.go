// SPDX-License-Identifier: Apache-2.0

package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/adiadia/agent-runtime/internal/metrics"
	"github.com/adiadia/agent-runtime/internal/transport/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const headerIdempotencyKey = "Idempotency-Key"

type createRunRequest struct {
	WebhookURL   string `json:"webhook_url"`
	Priority     int    `json:"priority"`
	TemplateName string `json:"template_name"`
}

type createAPIKeyRequest struct {
	Name              string `json:"name"`
	MaxConcurrentRuns int    `json:"max_concurrent_runs"`
	MaxRequestsPerMin int    `json:"max_requests_per_min"`
}

type Deps struct {
	RunRepo        RunCreator
	StepRepo       StepLister
	EventRepo      EventStreamer
	APIKeyAdmin    APIKeyManager
	Logger         *slog.Logger
	APIKeyResolver APIKeyResolver
	AdminToken     string
	Version        string
	Commit         string
	BuildDate      string
}

func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	metrics.Init()
	version := valueOrDefault(deps.Version, "dev")
	commit := valueOrDefault(deps.Commit, "none")
	buildDate := valueOrDefault(deps.BuildDate, "unknown")

	r := chi.NewRouter()
	r.Use(requestIDMiddleware())
	r.Use(requestLoggingMiddleware(logger))

	// ---------------- HEALTH ----------------

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("health check hit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ---------------- METRICS ----------------

	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		promhttp.Handler().ServeHTTP(w, r)
	})

	// ---------------- VERSION ----------------

	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"version":    version,
			"commit":     commit,
			"build_date": buildDate,
		})
	})

	// ---------------- API KEY LIFECYCLE (ADMIN) ----------------

	if deps.APIKeyAdmin != nil {
		r.Route("/api-keys", func(admin chi.Router) {
			admin.Use(middleware.AdminTokenAuth(deps.AdminToken, logger))

			admin.Post("/", func(w http.ResponseWriter, r *http.Request) {
				reqBody, err := decodeCreateAPIKeyRequest(r)
				if err != nil {
					http.Error(w, "invalid request body", http.StatusBadRequest)
					return
				}

				created, err := deps.APIKeyAdmin.CreateAPIKey(r.Context(), domain.CreateAPIKeyParams{
					Name:              reqBody.Name,
					MaxConcurrentRuns: reqBody.MaxConcurrentRuns,
					MaxRequestsPerMin: reqBody.MaxRequestsPerMin,
				})
				if err != nil {
					if errors.Is(err, domain.ErrInvalidAPIKeyName) {
						http.Error(w, "invalid api key name", http.StatusBadRequest)
						return
					}
					logger.Error("create api key failed", "error", err)
					http.Error(w, "failed to create api key", http.StatusInternalServerError)
					return
				}

				writeJSON(w, http.StatusOK, map[string]string{
					"api_key_id": created.ID.String(),
					"token":      created.Token,
				})
			})

			admin.Get("/", func(w http.ResponseWriter, r *http.Request) {
				keys, err := deps.APIKeyAdmin.ListAPIKeys(r.Context())
				if err != nil {
					logger.Error("list api keys failed", "error", err)
					http.Error(w, "failed to list api keys", http.StatusInternalServerError)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"api_keys": keys,
				})
			})

			admin.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) {
				id, err := uuid.Parse(chi.URLParam(r, "id"))
				if err != nil {
					http.Error(w, "invalid api key ID", http.StatusBadRequest)
					return
				}

				if err := deps.APIKeyAdmin.RevokeAPIKey(r.Context(), id); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						http.Error(w, "api key not found", http.StatusNotFound)
						return
					}
					logger.Error("delete api key failed", "api_key_id", id, "error", err)
					http.Error(w, "failed to delete api key", http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusNoContent)
			})
		})
	}

	// ---------------- RUNS (API KEY AUTH) ----------------

	r.Group(func(r chi.Router) {
		if deps.APIKeyResolver != nil {
			r.Use(middleware.APITokenAuth(deps.APIKeyResolver, logger))
		}

		// ---------------- CREATE RUN ----------------

		r.Post("/runs", func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if key := strings.TrimSpace(r.Header.Get(headerIdempotencyKey)); key != "" {
				ctx = auth.WithIdempotencyKey(ctx, key)
			}

			reqBody, err := decodeCreateRunRequest(r)
			if err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}

			runID, err := deps.RunRepo.CreateRun(ctx, domain.CreateRunParams{
				WebhookURL:   reqBody.WebhookURL,
				Priority:     reqBody.Priority,
				TemplateName: reqBody.TemplateName,
			})
			if err != nil {
				if errors.Is(err, domain.ErrMaxConcurrentRunsExceeded) {
					if w.Header().Get("Retry-After") == "" {
						w.Header().Set("Retry-After", "1")
					}
					http.Error(w, "max concurrent runs exceeded", http.StatusTooManyRequests)
					return
				}
				if errors.Is(err, domain.ErrWorkflowTemplateNotFound) {
					http.Error(w, "workflow template not found", http.StatusBadRequest)
					return
				}

				logger.Error("create run failed", "error", err)
				http.Error(w, "failed to create run", http.StatusInternalServerError)
				return
			}

			logger.Info("run created via API", "run_id", runID)

			writeJSON(w, http.StatusOK, map[string]string{
				"run_id": runID.String(),
			})
		})

		// ---------------- GET RUN COST ----------------

		r.Get("/runs/{id}/cost", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			breakdown, err := deps.RunRepo.GetRunCost(r.Context(), runID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("run not found", "run_id", runID)
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}

				logger.Error("get run cost failed", "run_id", runID, "error", err)
				http.Error(w, "failed to get run cost", http.StatusInternalServerError)
				return
			}

			writeJSON(w, http.StatusOK, breakdown)
		})

		// ---------------- GET RUN ----------------

		r.Get("/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			status, err := deps.RunRepo.GetRun(r.Context(), runID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("run not found", "run_id", runID)
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}

				logger.Error("get run failed", "run_id", runID, "error", err)
				http.Error(w, "failed to get run", http.StatusInternalServerError)
				return
			}

			writeJSON(w, http.StatusOK, map[string]string{
				"id":     runID.String(),
				"status": string(status), // convert domain type to string
			})
		})

		// ---------------- CANCEL RUN ----------------

		r.Post("/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			if err := deps.RunRepo.CancelRun(r.Context(), runID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("run not found", "run_id", runID)
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}

				logger.Error("cancel run failed", "run_id", runID, "error", err)
				http.Error(w, "failed to cancel run", http.StatusInternalServerError)
				return
			}

			logger.Info("run canceled via API", "run_id", runID)

			writeJSON(w, http.StatusOK, map[string]string{
				"id":     runID.String(),
				"status": string(domain.RunCanceled),
			})
		})

		// ---------------- LIST STEPS ----------------

		r.Get("/runs/{id}/steps", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			steps, err := deps.StepRepo.ListSteps(r.Context(), runID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("run not found", "run_id", runID)
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}

				logger.Error("list steps failed", "run_id", runID, "error", err)
				http.Error(w, "failed to list steps", http.StatusInternalServerError)
				return
			}

			writeJSON(w, http.StatusOK, struct {
				RunID string              `json:"run_id"`
				Steps []domain.StepRecord `json:"steps"`
			}{
				RunID: runID.String(),
				Steps: steps,
			})
		})

		// ---------------- STREAM EVENTS (SSE) ----------------

		r.Get("/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			// Enforce tenant ownership and hide cross-tenant existence.
			if _, err := deps.RunRepo.GetRun(r.Context(), runID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}
				logger.Error("sse get run failed", "run_id", runID, "error", err)
				http.Error(w, "failed to stream events", http.StatusInternalServerError)
				return
			}

			if deps.EventRepo == nil {
				logger.Error("sse events repository is not configured")
				http.Error(w, "failed to stream events", http.StatusInternalServerError)
				return
			}

			since := strings.TrimSpace(r.URL.Query().Get("since_id"))
			cursor, err := resolveEventsCursor(r.Context(), deps.EventRepo, runID, since)
			if err != nil {
				if errors.Is(err, errInvalidSinceID) {
					http.Error(w, "invalid since_id", http.StatusBadRequest)
					return
				}
				logger.Error("resolve events cursor failed",
					"run_id", runID,
					"since_id", since,
					"error", err,
				)
				http.Error(w, "failed to stream events", http.StatusInternalServerError)
				return
			}

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()

			writeEvents := func() error {
				events, err := deps.EventRepo.ListEventsAfter(r.Context(), runID, cursor)
				if err != nil {
					return err
				}

				for _, ev := range events {
					payload, err := json.Marshal(ev)
					if err != nil {
						return err
					}
					if _, err := fmt.Fprintf(w, "event: step_update\ndata: %s\n\n", payload); err != nil {
						return err
					}
					flusher.Flush()
					cursor = ev.Seq
				}

				return nil
			}

			if err := writeEvents(); err != nil {
				logger.Error("sse initial write failed", "run_id", runID, "error", err)
				return
			}

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					if err := writeEvents(); err != nil {
						logger.Error("sse write failed", "run_id", runID, "error", err)
						return
					}
				}
			}
		})

		// ---------------- APPROVE RUN ----------------

		r.Post("/runs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
			idStr := chi.URLParam(r, "id")

			runID, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid run ID", http.StatusBadRequest)
				return
			}

			if err := deps.RunRepo.ApproveRun(r.Context(), runID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("run not found", "run_id", runID)
					http.Error(w, "run not found", http.StatusNotFound)
					return
				}

				logger.Error("approve run failed", "run_id", runID, "error", err)
				http.Error(w, "failed to approve run", http.StatusInternalServerError)
				return
			}

			logger.Info("run approved via API", "run_id", runID)

			writeJSON(w, http.StatusOK, map[string]string{
				"id":     runID.String(),
				"status": "APPROVED",
			})
		})
	})

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeCreateRunRequest(r *http.Request) (createRunRequest, error) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return createRunRequest{}, nil
	}

	var req createRunRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return createRunRequest{}, nil
		}
		return createRunRequest{}, err
	}

	// Ensure there is only one JSON object.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return createRunRequest{}, errors.New("request body must contain exactly one JSON object")
	}

	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	req.TemplateName = strings.TrimSpace(req.TemplateName)
	if req.WebhookURL == "" {
		return req, nil
	}

	parsed, err := url.Parse(req.WebhookURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return createRunRequest{}, errors.New("invalid webhook_url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return createRunRequest{}, errors.New("unsupported webhook_url scheme")
	}

	return req, nil
}

func decodeCreateAPIKeyRequest(r *http.Request) (createAPIKeyRequest, error) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return createAPIKeyRequest{}, domain.ErrInvalidAPIKeyName
	}

	var req createAPIKeyRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return createAPIKeyRequest{}, err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return createAPIKeyRequest{}, errors.New("request body must contain exactly one JSON object")
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return createAPIKeyRequest{}, domain.ErrInvalidAPIKeyName
	}

	return req, nil
}

var errInvalidSinceID = errors.New("invalid since_id")

func resolveEventsCursor(
	ctx context.Context,
	eventRepo EventStreamer,
	runID uuid.UUID,
	since string,
) (int64, error) {
	if since == "" {
		return 0, nil
	}

	if seq, err := strconv.ParseInt(since, 10, 64); err == nil {
		if seq < 0 {
			return 0, errInvalidSinceID
		}
		return seq, nil
	}

	eventID, err := uuid.Parse(since)
	if err != nil {
		return 0, errInvalidSinceID
	}

	seq, err := eventRepo.ResolveCursorByEventID(ctx, runID, eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errInvalidSinceID
		}
		return 0, err
	}

	return seq, nil
}

func valueOrDefault(value, defaultValue string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue
	}
	return trimmed
}
