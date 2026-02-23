// SPDX-License-Identifier: Apache-2.0

package domain

type RunStatus string

const (
	RunPending  RunStatus = "PENDING"
	RunRunning  RunStatus = "RUNNING"
	RunWaiting  RunStatus = "WAITING_APPROVAL"
	RunSuccess  RunStatus = "SUCCEEDED"
	RunFailed   RunStatus = "FAILED"
	RunCanceled RunStatus = "CANCELED"
)

type CreateRunParams struct {
	WebhookURL   string
	Priority     int
	TemplateName string
}
