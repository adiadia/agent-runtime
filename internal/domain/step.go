package domain

import "github.com/google/uuid"

type StepStatus string
type StepName string

type StepRecord struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
}

const (
	StepPending  StepStatus = "PENDING"
	StepRunning  StepStatus = "RUNNING"
	StepWaiting  StepStatus = "WAITING_APPROVAL"
	StepSuccess  StepStatus = "SUCCEEDED"
	StepFailed   StepStatus = "FAILED"
	StepCanceled StepStatus = "CANCELED"
)

const (
	StepLLM      StepName = "LLM"
	StepTool     StepName = "TOOL"
	StepApproval StepName = "APPROVAL"
)
