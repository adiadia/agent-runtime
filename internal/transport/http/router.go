package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Deps struct {
	RunRepo  RunCreator
	StepRepo StepLister
	Logger   *slog.Logger
}

func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()

	// ---------------- HEALTH ----------------

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("health check hit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ---------------- CREATE RUN ----------------

	r.Post("/runs", func(w http.ResponseWriter, r *http.Request) {
		runID, err := deps.RunRepo.CreateRun(r.Context())
		if err != nil {
			logger.Error("create run failed", "error", err)
			http.Error(w, "failed to create run", http.StatusInternalServerError)
			return
		}

		logger.Info("run created via API", "run_id", runID)

		writeJSON(w, http.StatusOK, map[string]string{
			"run_id": runID.String(),
		})
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
			logger.Warn("run not found", "run_id", runID)
			http.Error(w, "run not found", http.StatusNotFound)
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

	// ---------------- APPROVE RUN ----------------

	r.Post("/runs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")

		runID, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "invalid run ID", http.StatusBadRequest)
			return
		}

		if err := deps.RunRepo.ApproveRun(r.Context(), runID); err != nil {
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

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
