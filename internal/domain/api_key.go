// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	DefaultMaxConcurrentRuns = 5
	DefaultMaxRequestsPerMin = 60
)

type CreateAPIKeyParams struct {
	Name              string
	MaxConcurrentRuns int
	MaxRequestsPerMin int
}

type CreatedAPIKey struct {
	ID    uuid.UUID
	Token string
}

type APIKeyRecord struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	MaxConcurrentRuns int       `json:"max_concurrent_runs"`
	MaxRequestsPerMin int       `json:"max_requests_per_min"`
	CreatedAt         time.Time `json:"created_at"`
}
