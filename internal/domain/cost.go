// SPDX-License-Identifier: Apache-2.0

package domain

import "github.com/google/uuid"

type StepCostBreakdown struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Status  string    `json:"status"`
	CostUSD float64   `json:"cost_usd"`
}

type RunCostBreakdown struct {
	RunID        uuid.UUID           `json:"run_id"`
	TotalCostUSD float64             `json:"total_cost_usd"`
	Steps        []StepCostBreakdown `json:"steps"`
}
