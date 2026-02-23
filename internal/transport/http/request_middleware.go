// SPDX-License-Identifier: Apache-2.0

package httptransport

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/google/uuid"
)

const headerRequestID = "X-Request-Id"

type requestIDContextKey struct{}

var ctxRequestIDKey requestIDContextKey

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Flush() {
	flusher, ok := s.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxRequestIDKey, requestID)
}

func requestIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxRequestIDKey).(string)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

func requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := strings.TrimSpace(r.Header.Get(headerRequestID))
			if reqID == "" {
				reqID = uuid.NewString()
			}

			w.Header().Set(headerRequestID, reqID)
			next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), reqID)))
		})
	}
}

func requestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			reqID, _ := requestIDFromContext(r.Context())
			attrs := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			}
			if apiKeyID, ok := auth.APIKeyIDFromContext(r.Context()); ok {
				attrs = append(attrs, "api_key_id", apiKeyID)
			}

			logger.Info("request completed", attrs...)
		})
	}
}
