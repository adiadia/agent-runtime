# Problem Statement

## Why this project exists
Many agent systems start as simple in-process loops: receive request, call model/tool, return result. That works for short tasks, but it breaks down for production workflows that are:

- Long-running (minutes to hours)
- Multi-step (LLM + tools + approvals)
- Failure-prone (network outages, model/tool errors, deploy restarts)
- Multi-tenant (different customers, different limits)
- Auditable (who approved what, when, and why)

Agent Runtime exists to make those workflows durable and operationally safe without requiring every team to build workflow infrastructure from scratch.

## Pain points in real systems
Teams building agent automation repeatedly hit the same issues:

- Lost progress on restarts: in-memory state disappears when process/container dies.
- Duplicate execution: retries at the HTTP layer can create duplicate runs.
- No safe human-in-the-loop path: approval steps become ad-hoc and hard to reason about.
- Weak observability: hard to answer "what happened to run X?".
- Tenant isolation gaps: one customer can accidentally affect another.
- Backpressure issues: no per-tenant concurrency/rate limits.

## Why naive in-memory orchestrators fail
A naive orchestrator usually keeps status in RAM and a goroutine queue. This design fails in production:

| Failure mode | What happens in memory-only design | Production impact |
|---|---|---|
| Process crash/redeploy | State is lost | Runs stall or silently disappear |
| Worker restart mid-step | No reclaim logic | Steps remain "stuck" forever |
| API retries/timeouts | No idempotency key tracking | Duplicate runs and side effects |
| Human approval | No durable waiting state | Approvals race or are lost |
| Multi-tenant scaling | Shared queue with weak guards | Noisy-neighbor incidents |
| Auditing/compliance | No immutable event timeline | Hard incident analysis |

Durability, retries, and auditability need a persistent state model.

## Scope and non-goals
This project is intentionally pragmatic.

### In scope
- Durable run/step state in Postgres
- Retry scheduling with exponential backoff
- Human approval step handling
- Multi-tenant isolation by `api_key_id`
- Event streaming (SSE) and webhooks
- Per-tenant limits and observability

### Non-goals (current)
- Fully replacing Temporal/Cadence for advanced orchestration use cases
- Full DAG execution and arbitrary graph scheduling (current model is ordered template steps)
- Cross-region consensus workflow engine
- A complete policy engine for enterprise governance (RBAC depth is roadmap work)

## Target audience
Agent Runtime is designed for teams that need reliability before building platform internals:

- LangChain and agent-framework users moving from prototype to production
- Internal platform teams standardizing durable automation execution
- Automation and operations teams running long-lived workflows with approvals
- Product teams serving multiple tenants with clear isolation and limits

## A concrete example
A typical run may look like:

1. `LLM` proposes an action plan.
2. `TOOL` executes the action.
3. `APPROVAL` waits for human confirmation.
4. On approval, run completes.

If the worker crashes after step 2, the system still recovers because step/run state and events are persisted and reclaim/retry logic is durable.
