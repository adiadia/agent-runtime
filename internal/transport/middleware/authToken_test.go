// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/google/uuid"
)

func TestAPITokenAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKeyID := uuid.New()

	t.Run("allows healthz path without auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d got %d", http.StatusOK, rec.Code)
		}
	})

	t.Run("allows metrics path without auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d got %d", http.StatusOK, rec.Code)
		}
	})

	t.Run("allows version path without auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/version", nil)
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d got %d", http.StatusOK, rec.Code)
		}
	})

	t.Run("rejects missing token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/runs", nil)
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status %d got %d", http.StatusUnauthorized, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("expected WWW-Authenticate header %q got %q", "Bearer", got)
		}
	})

	t.Run("rejects unknown token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/runs", nil)
		req.Header.Set("Authorization", "Bearer nope")
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status %d got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("resolver error returns internal server error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/runs", nil)
		req.Header.Set("Authorization", "Bearer super-secret")
		rec := httptest.NewRecorder()

		APITokenAuth(&mockAPIKeyResolver{err: errors.New("db down")}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status %d got %d", http.StatusInternalServerError, rec.Code)
		}
	})

	t.Run("accepts valid token and sets api key id in context", func(t *testing.T) {
		resolver := &mockAPIKeyResolver{
			keyByToken: map[string]auth.APIKey{
				"super-secret": {
					ID:                apiKeyID,
					MaxConcurrentRuns: 5,
					MaxRequestsPerMin: 60,
				},
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/runs", nil)
		req.Header.Set("Authorization", "Bearer super-secret")
		rec := httptest.NewRecorder()

		APITokenAuth(resolver, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.APIKeyIDFromContext(r.Context())
			if !ok {
				t.Fatal("expected api key id in request context")
			}
			if id != apiKeyID {
				t.Fatalf("expected api key id %s got %s", apiKeyID, id)
			}
			if got := w.Header().Get(headerRateLimitLimit); got != "60" {
				t.Fatalf("expected %s header %q got %q", headerRateLimitLimit, "60", got)
			}
			if got := w.Header().Get(headerRateLimitRemaining); got == "" {
				t.Fatalf("expected %s header to be set", headerRateLimitRemaining)
			}
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d got %d", http.StatusOK, rec.Code)
		}
	})

	t.Run("rate limits per api key and sets retry header", func(t *testing.T) {
		resolver := &mockAPIKeyResolver{
			keyByToken: map[string]auth.APIKey{
				"low-limit": {
					ID:                uuid.New(),
					MaxConcurrentRuns: 5,
					MaxRequestsPerMin: 1,
				},
			},
		}

		handler := APITokenAuth(resolver, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req1 := httptest.NewRequest(http.MethodGet, "/runs", nil)
		req1.Header.Set("Authorization", "Bearer low-limit")
		rec1 := httptest.NewRecorder()
		handler.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusOK {
			t.Fatalf("expected first request status 200 got %d", rec1.Code)
		}
		if got := rec1.Header().Get(headerRateLimitLimit); got != "1" {
			t.Fatalf("expected first %s header %q got %q", headerRateLimitLimit, "1", got)
		}
		if got := rec1.Header().Get(headerRateLimitRemaining); got != "0" {
			t.Fatalf("expected first %s header %q got %q", headerRateLimitRemaining, "0", got)
		}

		req2 := httptest.NewRequest(http.MethodGet, "/runs", nil)
		req2.Header.Set("Authorization", "Bearer low-limit")
		rec2 := httptest.NewRecorder()
		handler.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusTooManyRequests {
			t.Fatalf("expected second request status 429 got %d", rec2.Code)
		}
		if got := rec2.Header().Get(headerRateLimitLimit); got != "1" {
			t.Fatalf("expected second %s header %q got %q", headerRateLimitLimit, "1", got)
		}
		if got := rec2.Header().Get(headerRateLimitRemaining); got != "0" {
			t.Fatalf("expected second %s header %q got %q", headerRateLimitRemaining, "0", got)
		}
		retryAfter := rec2.Header().Get(headerRetryAfter)
		if retryAfter == "" {
			t.Fatalf("expected %s header to be set", headerRetryAfter)
		}
		if _, err := strconv.Atoi(retryAfter); err != nil {
			t.Fatalf("expected numeric %s header, got %q", headerRetryAfter, retryAfter)
		}
	})
}

func TestAPITokenAuthPanicsWithoutToken(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected APITokenAuth to panic when resolver is nil")
		}
	}()

	APITokenAuth(nil, nil)
}

func TestBearerToken(t *testing.T) {
	if got, ok := bearerToken("Bearer secret"); !ok || got != "secret" {
		t.Fatal("expected exact bearer token to be valid")
	}
	if got, ok := bearerToken("bearer secret"); !ok || got != "secret" {
		t.Fatal("expected bearer scheme to be case-insensitive")
	}
	if _, ok := bearerToken("Token secret"); ok {
		t.Fatal("expected non-bearer scheme to be invalid")
	}
	if _, ok := bearerToken("Bearer"); ok {
		t.Fatal("expected malformed header to be invalid")
	}
	if _, ok := bearerToken("Bearer "); ok {
		t.Fatal("expected empty token to be invalid")
	}
}

type mockAPIKeyResolver struct {
	keyByToken map[string]auth.APIKey
	err        error
}

func (m *mockAPIKeyResolver) ResolveAPIKey(ctx context.Context, bearerToken string) (auth.APIKey, bool, error) {
	if m.err != nil {
		return auth.APIKey{}, false, m.err
	}

	if key, ok := m.keyByToken[bearerToken]; ok {
		return key, true, nil
	}

	return auth.APIKey{}, false, nil
}
