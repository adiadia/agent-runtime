// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminTokenAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("rejects when admin token is not configured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api-keys", nil)
		rec := httptest.NewRecorder()

		AdminTokenAuth("", logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status %d got %d", http.StatusInternalServerError, rec.Code)
		}
	})

	t.Run("rejects missing token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api-keys", nil)
		rec := httptest.NewRecorder()

		AdminTokenAuth("admin-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status %d got %d", http.StatusUnauthorized, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("expected WWW-Authenticate header %q got %q", "Bearer", got)
		}
	})

	t.Run("rejects wrong token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api-keys", nil)
		req.Header.Set("Authorization", "Bearer nope")
		rec := httptest.NewRecorder()

		AdminTokenAuth("admin-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status %d got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("accepts valid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api-keys", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		rec := httptest.NewRecorder()

		AdminTokenAuth("admin-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d got %d", http.StatusOK, rec.Code)
		}
	})
}
