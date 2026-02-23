// SPDX-License-Identifier: Apache-2.0

package httptransport

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddlewareGeneratesAndPropagatesRequestID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var gotRequestID string
	h := requestIDMiddleware()(requestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, ok := requestIDFromContext(r.Context())
		if !ok {
			t.Fatal("expected request_id in context")
		}
		gotRequestID = requestID
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	respRequestID := rec.Header().Get(headerRequestID)
	if respRequestID == "" {
		t.Fatal("expected X-Request-Id response header")
	}
	if gotRequestID != respRequestID {
		t.Fatalf("expected context request_id %q got %q", respRequestID, gotRequestID)
	}
}

func TestRequestIDMiddlewarePreservesIncomingRequestID(t *testing.T) {
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, ok := requestIDFromContext(r.Context())
		if !ok {
			t.Fatal("expected request_id in context")
		}
		if requestID != "req-fixed-id" {
			t.Fatalf("expected request_id req-fixed-id got %q", requestID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(headerRequestID, "req-fixed-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 got %d", rec.Code)
	}
	if got := rec.Header().Get(headerRequestID); got != "req-fixed-id" {
		t.Fatalf("expected X-Request-Id req-fixed-id got %q", got)
	}
}
