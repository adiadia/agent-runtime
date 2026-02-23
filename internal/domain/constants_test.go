// SPDX-License-Identifier: Apache-2.0

package domain

import "testing"

func TestRunStatusConstants(t *testing.T) {
	if RunPending != "PENDING" {
		t.Fatalf("unexpected RunPending value: %s", RunPending)
	}
	if RunRunning != "RUNNING" {
		t.Fatalf("unexpected RunRunning value: %s", RunRunning)
	}
	if RunWaiting != "WAITING_APPROVAL" {
		t.Fatalf("unexpected RunWaiting value: %s", RunWaiting)
	}
	if RunSuccess != "SUCCEEDED" {
		t.Fatalf("unexpected RunSuccess value: %s", RunSuccess)
	}
	if RunFailed != "FAILED" {
		t.Fatalf("unexpected RunFailed value: %s", RunFailed)
	}
	if RunCanceled != "CANCELED" {
		t.Fatalf("unexpected RunCanceled value: %s", RunCanceled)
	}
}

func TestStepConstants(t *testing.T) {
	if StepPending != "PENDING" {
		t.Fatalf("unexpected StepPending value: %s", StepPending)
	}
	if StepRunning != "RUNNING" {
		t.Fatalf("unexpected StepRunning value: %s", StepRunning)
	}
	if StepWaiting != "WAITING_APPROVAL" {
		t.Fatalf("unexpected StepWaiting value: %s", StepWaiting)
	}
	if StepSuccess != "SUCCEEDED" {
		t.Fatalf("unexpected StepSuccess value: %s", StepSuccess)
	}
	if StepFailed != "FAILED" {
		t.Fatalf("unexpected StepFailed value: %s", StepFailed)
	}
	if StepCanceled != "CANCELED" {
		t.Fatalf("unexpected StepCanceled value: %s", StepCanceled)
	}

	if StepLLM != "LLM" {
		t.Fatalf("unexpected StepLLM value: %s", StepLLM)
	}
	if StepTool != "TOOL" {
		t.Fatalf("unexpected StepTool value: %s", StepTool)
	}
	if StepApproval != "APPROVAL" {
		t.Fatalf("unexpected StepApproval value: %s", StepApproval)
	}
}
