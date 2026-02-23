// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"errors"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/google/uuid"
)

var ErrMissingAPIKeyID = errors.New("missing api key id in context")

func apiKeyIDFromContext(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.APIKeyIDFromContext(ctx)
	if !ok {
		return uuid.Nil, ErrMissingAPIKeyID
	}
	return id, nil
}
