# State Machine

This document defines run/step state semantics, allowed transitions, and important invariants.

## State overview (ASCII)

```text
Run state (high-level):
  PENDING --> RUNNING --> (WAITING_APPROVAL) --> SUCCEEDED
                      \\                         ^
                       \\--> FAILED ------------/
                        \\--> CANCELED (terminal)

Step state:
  PENDING --> RUNNING --> SUCCEEDED
                |   \\--> PENDING (retry with backoff)
                |    \\-> FAILED (attempts exhausted)
                \\----> CANCELED

Approval step:
  PENDING --> WAITING_APPROVAL --> SUCCEEDED
```

## Enums

### RunStatus
- `PENDING`
- `RUNNING`
- `WAITING_APPROVAL`
- `SUCCEEDED`
- `FAILED`
- `CANCELED`

### StepStatus
- `PENDING`
- `RUNNING`
- `WAITING_APPROVAL`
- `SUCCEEDED`
- `FAILED`
- `CANCELED`

## Run transitions

| From | To | Trigger | Notes |
|---|---|---|---|
| `PENDING` | `RUNNING` | Worker claims first runnable step | First durable execution transition |
| `RUNNING` | `WAITING_APPROVAL` | Approval gate reached | Supported domain state; current flow primarily tracks waiting at step level |
| `WAITING_APPROVAL` | `RUNNING` | `POST /runs/{id}/approve` | Approval resumes workflow |
| `RUNNING` | `SUCCEEDED` | All steps are `SUCCEEDED` | Terminal |
| `WAITING_APPROVAL` | `SUCCEEDED` | Approval completes last pending gate | Terminal |
| `RUNNING` | `FAILED` | Step exhausts retries / terminal failure | Terminal |
| `WAITING_APPROVAL` | `FAILED` | Failure on remaining execution after approval | Terminal |
| `PENDING` / `RUNNING` / `WAITING_APPROVAL` | `CANCELED` | `POST /runs/{id}/cancel` | Terminal |

Implementation note:
- The approval wait is durably tracked at step level (`APPROVAL` step in `WAITING_APPROVAL`).
- `RunStatus=WAITING_APPROVAL` is part of the domain model; current implementation often keeps run status as `RUNNING` while the approval step waits.

## Step transitions

| From | To | Trigger | Notes |
|---|---|---|---|
| `PENDING` | `RUNNING` | Worker claim | Increments `attempts` |
| `RUNNING` | `SUCCEEDED` | Executor success | Stores output and cost |
| `RUNNING` | `PENDING` | Retryable failure and attempts remaining | Sets `next_run_at` with exponential backoff |
| `RUNNING` | `FAILED` | Attempts exhausted | Terminal step failure |
| `PENDING` | `WAITING_APPROVAL` | Tool stage completed, approval step opened | Human gate |
| `WAITING_APPROVAL` | `SUCCEEDED` | Approve endpoint | Worker does not execute approval |
| `PENDING` / `RUNNING` / `WAITING_APPROVAL` | `CANCELED` | Run cancel | Terminal |
| `RUNNING` (stale) | `RUNNING` (reclaimed) | Claim reclaim logic | Allowed when `started_at` is older than reclaim threshold |

## Invariants

### Approval only from `WAITING_APPROVAL`
- Approve operation updates only approval step rows in `WAITING_APPROVAL`.
- Approve is idempotent: if no waiting approval step exists, it does not incorrectly advance state.

### Worker never executes `APPROVAL`
- Claim query excludes step name `APPROVAL` from execution selection.
- Approval progression is API-driven (`POST /runs/{id}/approve`).

### Ordering constraints from templates
- Steps are created in template position order.
- Worker only claims a step when all earlier steps in the same run are already `SUCCEEDED`.
- This enforces strict sequential execution for current template model.

## Edge cases and behavior

### Reclaiming stale `RUNNING` steps
- Worker can reclaim a `RUNNING` step if `started_at` is older than `reclaim_after`.
- Prevents permanent stalls after worker crash or network partitions.

### Retries and exponential backoff
- On retryable failure, step returns to `PENDING`.
- `attempts` drives next schedule: `next_run_at = now + (2^attempts * base_delay)`.
- When attempts exceed max, step becomes `FAILED` and run transitions to `FAILED`.

### Idempotent run creation
- `POST /runs` supports `Idempotency-Key`.
- `(api_key_id, idempotency_key)` uniqueness guarantees the same request maps to one durable run id.

### Tenant isolation edge case
- Access checks for get/list/cancel/approve are tenant-scoped.
- Cross-tenant resource access intentionally returns `404` to avoid existence leaks.
