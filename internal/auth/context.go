// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"

	"github.com/google/uuid"
)

type apiKeyIDContextKey struct{}
type apiKeyContextKey struct{}
type idempotencyKeyContextKey struct{}

var ctxAPIKeyIDKey apiKeyIDContextKey
var ctxAPIKeyKey apiKeyContextKey
var ctxIdempotencyKey idempotencyKeyContextKey

type APIKey struct {
	ID                uuid.UUID
	MaxConcurrentRuns int
	MaxRequestsPerMin int
}

// WithAPIKeyID stores the authenticated tenant id on the request context.
func WithAPIKeyID(ctx context.Context, apiKeyID uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxAPIKeyIDKey, apiKeyID)
}

// WithAPIKey stores the resolved API key and limits on request context.
func WithAPIKey(ctx context.Context, key APIKey) context.Context {
	ctx = context.WithValue(ctx, ctxAPIKeyKey, key)
	return context.WithValue(ctx, ctxAPIKeyIDKey, key.ID)
}

// APIKeyIDFromContext reads the authenticated tenant id from context.
func APIKeyIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if key, ok := APIKeyFromContext(ctx); ok {
		return key.ID, true
	}

	v := ctx.Value(ctxAPIKeyIDKey)
	id, ok := v.(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// APIKeyFromContext reads the resolved API key and limits from context.
func APIKeyFromContext(ctx context.Context) (APIKey, bool) {
	v := ctx.Value(ctxAPIKeyKey)
	key, ok := v.(APIKey)
	if !ok || key.ID == uuid.Nil {
		return APIKey{}, false
	}
	return key, true
}

func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxIdempotencyKey, key)
}

func IdempotencyKeyFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(ctxIdempotencyKey)
	key, ok := v.(string)
	if !ok || key == "" {
		return "", false
	}
	return key, true
}
