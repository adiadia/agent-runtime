// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// AdminTokenAuth enforces a master admin bearer token for protected routes.
func AdminTokenAuth(adminToken string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSpace(adminToken) == "" {
				logger.Error("admin token not configured")
				http.Error(w, "admin auth not configured", http.StatusInternalServerError)
				return
			}

			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "missing or invalid admin token", http.StatusUnauthorized)
				return
			}

			if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "missing or invalid admin token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
