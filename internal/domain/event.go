// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventRecord struct {
	ID        uuid.UUID       `json:"id"`
	Seq       int64           `json:"seq"`
	RunID     uuid.UUID       `json:"run_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}
