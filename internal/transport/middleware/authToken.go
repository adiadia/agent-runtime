// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adiadia/agent-runtime/internal/auth"
)

const healthzPath = "/healthz"
const metricsPath = "/metrics"
const versionPath = "/version"
const headerRateLimitLimit = "X-RateLimit-Limit"
const headerRateLimitRemaining = "X-RateLimit-Remaining"
const headerRetryAfter = "Retry-After"

type APIKeyResolver interface {
	ResolveAPIKey(ctx context.Context, bearerToken string) (auth.APIKey, bool, error)
}

// APITokenAuth enforces bearer-token authentication for all routes except
// /healthz, /metrics, and /version; resolves api_key_id from token, and stores
// it on request context.
func APITokenAuth(resolver APIKeyResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return apiTokenAuthWithLimiter(resolver, newInMemoryRateLimiter(), logger)
}

func apiTokenAuthWithLimiter(
	resolver APIKeyResolver,
	limiter *inMemoryRateLimiter,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	if resolver == nil {
		panic("middleware.APITokenAuth requires a resolver")
	}
	if limiter == nil {
		panic("middleware.APITokenAuth requires a limiter")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == healthzPath || r.URL.Path == metricsPath || r.URL.Path == versionPath {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			token, ok := bearerToken(authHeader)
			if !ok {
				logger.Warn("request blocked by api token middleware",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
				)
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "missing or invalid API token", http.StatusUnauthorized)
				return
			}

			key, found, err := resolver.ResolveAPIKey(r.Context(), token)
			if err != nil {
				logger.Error("api key resolution failed",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"error", err,
				)
				http.Error(w, "auth lookup failed", http.StatusInternalServerError)
				return
			}

			if !found {
				logger.Warn("request blocked by api key lookup",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
				)
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "missing or invalid API token", http.StatusUnauthorized)
				return
			}

			decision := limiter.Allow(key.ID, key.MaxRequestsPerMin, time.Now())
			w.Header().Set(headerRateLimitLimit, strconv.Itoa(decision.LimitPerMinute))
			w.Header().Set(headerRateLimitRemaining, strconv.Itoa(decision.Remaining))
			if !decision.Allowed {
				w.Header().Set(headerRetryAfter, strconv.Itoa(decision.RetryAfterSeconds))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			// Preserve authenticated context on the current request pointer so
			// outer middleware (request logging) can read api_key_id after next returns.
			*r = *r.WithContext(auth.WithAPIKey(r.Context(), key))
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(header string) (string, bool) {
	schemeToken := strings.SplitN(header, " ", 2)
	if len(schemeToken) != 2 {
		return "", false
	}
	if !strings.EqualFold(schemeToken[0], "Bearer") {
		return "", false
	}
	if schemeToken[1] == "" {
		return "", false
	}
	return schemeToken[1], true
}
