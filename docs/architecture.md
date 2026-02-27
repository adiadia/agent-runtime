# Architecture

## High-level view

```text
                    +---------------------------+
                    |        Clients/SDKs       |
                    |  (CLI, backend, UI apps)  |
                    +-------------+-------------+
                                  |
                                  | HTTP + Bearer
                                  v
+----------------------------------------------------------------+
|                           API Service                           |
|----------------------------------------------------------------|
| Router                                                          |
|  - Request ID middleware (X-Request-Id)                        |
|  - Request logging middleware                                  |
|  - Admin token auth (/api-keys)                               |
|  - API key auth + rate limit (/runs/*)                        |
|  - Run/step/cost/event endpoints                              |
+-------------------------------+--------------------------------+
                                |
                                | SQL
                                v
+----------------------------------------------------------------+
|                             Postgres                            |
|----------------------------------------------------------------|
| api_keys | runs | steps | events | run_requests | templates    |
+-------------------------------+--------------------------------+
                                ^
                                |
                                | claim/execute/update/events
+-------------------------------+--------------------------------+
|                               Worker                            |
|----------------------------------------------------------------|
| - Claims runnable steps for one tenant (api_key_id)            |
| - Executes LLM/TOOL steps                                      |
| - Handles retries, reclaim, timeout                            |
| - Emits events, updates run/step state                         |
| - Sends terminal webhooks                                      |
+----------------------------------------------------------------+
```

## Request and execution flow (ASCII)

```text
Client
  |
  | 1) POST /runs (Bearer API token, optional Idempotency-Key)
  v
API Router
  |- request_id middleware (X-Request-Id)
  |- auth middleware (api key hash lookup, rate-limit headers)
  |- run creation + template expansion in DB transaction
  v
Postgres (runs + steps + run_requests + events)
  ^
  | 2) worker poll + claim
Worker (tenant-scoped by api_key_id)
  |- claim step ordered by priority/created_at
  |- execute LLM/TOOL
  |- retry/backoff or success/fail transitions
  |- insert events
  |- webhook on terminal run (optional X-Signature)
  v
Postgres
  ^
  | 3) SSE poll
API Router -> GET /runs/{id}/events -> stream step_update events
```

## Components

### API
- Exposes lifecycle APIs for runs, steps, approvals, cancelation, costs, and event streaming.
- Exposes admin APIs for API key lifecycle: `POST /api-keys`, `GET /api-keys`, `DELETE /api-keys/{id}`.
- `POST /runs` accepts optional `template_name`, `priority` (JSON integer), and `webhook_url`.
- Key runtime endpoints include:
  - `GET /runs/{id}`
  - `GET /runs/{id}/steps`
  - `GET /runs/{id}/events`
  - `GET /runs/{id}/cost`
  - `POST /runs/{id}/approve`
  - `POST /runs/{id}/cancel`
- Enforces ownership checks by `api_key_id` so cross-tenant resources return `404`.
- Health and metrics endpoints are public: `GET /healthz`, `GET /metrics`.
- `/healthz` returns `503` when required schema is missing and `200` only after schema checks pass.

### Authentication middleware
- Runtime endpoints (`/runs/*`) use Bearer API key auth.
- Admin endpoints (`/api-keys`) use a master `ADMIN_TOKEN`.
- API key bearer tokens are matched by SHA256 hash (`token_hash`) in DB.
- `/healthz` and `/metrics` do not require auth.

### Rate limiting
- In-memory token bucket per `api_key_id`.
- Limit source: `api_keys.max_requests_per_min`.
- Response headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`.
- Exceeded requests return `429 Too Many Requests`.

### Worker
- Dedicated per tenant (requires `--api-key-id` currently).
- Claims only that tenant's steps.
- Claim ordering: `runs.priority DESC`, then `steps.created_at ASC`.
- Pre-claim guard: skips claim when tenant running-step concurrency is already at limit.

### Executors
- Step executors for `LLM` and `TOOL`.
- `APPROVAL` is never executed by worker; it is transitioned via approve API.

### Postgres schema
Core durable tables:
- `api_keys`: tenant identity, hashed token, limits, revocation state.
- `runs`: per-workflow state, priority, webhook settings, total cost.
- `steps`: per-step state, attempts, retry schedule, timeout, cost.
- `events`: append-style timeline for stream/audit.
- `run_requests`: idempotency key mapping per tenant.
- `workflow_templates`, `workflow_template_steps`: template-defined ordered steps.
- `schema_migrations`: applied migration files tracked by startup bootstrap.

### Schema bootstrap
- API and worker startup paths run embedded SQL migrations in filename order.
- Migration execution is serialized with a Postgres advisory lock.
- Each migration is recorded in `schema_migrations` to keep restarts deterministic.

### SSE
- `GET /runs/{id}/events` streams incremental events.
- Polls DB for records after a cursor (`seq` or event `id`).

### Webhooks
- On terminal run states (`SUCCEEDED`, `FAILED`), worker can POST callback payload.
- Optional HMAC signature header (`X-Signature`) when secret exists.

## Multi-tenant model
Tenant boundary is `api_key_id`.

- Each run belongs to exactly one API key.
- Steps are scoped through their run.
- Worker claims are filtered by `runs.api_key_id`.
- API lookup/approve/cancel/list all enforce tenant ownership.
- Rate limits and concurrency are per tenant.

## Data model summary

| Table | Purpose | Important fields |
|---|---|---|
| `api_keys` | Tenant identity and limits | `id`, `name`, `token_hash`, `max_concurrent_runs`, `max_requests_per_min`, `revoked_at` |
| `runs` | Workflow instance | `id`, `api_key_id`, `status`, `priority`, `webhook_url`, `webhook_secret`, `total_cost_usd` |
| `steps` | Ordered run execution units | `id`, `run_id`, `name`, `status`, `attempts`, `next_run_at`, `timeout_seconds`, `cost_usd` |
| `events` | Event timeline for SSE/audit | `seq`, `id`, `run_id`, `step_id`, `type`, `payload`, `created_at` |
| `run_requests` | Idempotency map | `api_key_id`, `idempotency_key`, `run_id` (unique per tenant/key) |
| `workflow_templates` | Named workflow templates | `id`, `name` |
| `workflow_template_steps` | Ordered template steps | `template_id`, `position`, `name`, `timeout_seconds` |

## Deployment modes

### Dedicated worker per tenant (implemented)
- Run one worker process per `api_key_id`.
- Strong isolation and easy per-tenant scaling.
- Current `cmd/worker` enforces this mode via required `--api-key-id`.

### Shared worker pool (operational pattern / future mode)
- A supervisor can run many worker instances across tenants.
- Runtime model supports this by tenant-scoped claims.
- A single process that dynamically serves all tenants is a potential future enhancement.

## Observability
- Structured logging with `log/slog`.
- Per-request logs include request id, status, latency, and tenant id when available.
- Metrics endpoint: `GET /metrics` (Prometheus format).

## Security model
- API key raw tokens are returned only once at creation and never stored.
- Database stores only `SHA256` hash (`token_hash`).
- Admin key operations are protected by `ADMIN_TOKEN`.
- Tenant isolation is enforced in API and worker claim paths.
- Request tracing uses `X-Request-Id` for correlation across services/logs.
- Principle of least privilege: workers operate only on configured tenant scope.
