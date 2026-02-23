# Agent Runtime

## 1) What is Agent Runtime?
Agent Runtime is a durable execution service for multi-step agent workflows. Instead of keeping workflow state in memory, it persists run state, step state, retries, and events in Postgres so execution can survive restarts, worker failures, and redeploys.

It is designed for teams running production automation that needs reliability and control: retries with backoff, human approval gates, tenant isolation, audit/event streams, and webhook callbacks when runs finish.

## Documentation
- [Problem statement](docs/problem.md)
- [Architecture](docs/architecture.md)
- [State machine](docs/state-machine.md)
- [Roadmap](docs/roadmap.md)
- [Changelog](CHANGELOG.md)

## 2) Features
- Durable workflow state in Postgres (`runs`, `steps`, `events`)
- Retry scheduling with exponential backoff (`attempts`, `next_run_at`)
- Ordered workflow templates (default: `LLM -> TOOL -> APPROVAL`)
- Server-Sent Events stream (`GET /runs/{id}/events`)
- Terminal run webhooks with optional HMAC signature
- Cost tracking per step and per run (`GET /runs/{id}/cost`)
- Per-tenant auth and isolation by `api_key_id`
- Per-tenant request rate limiting and concurrent-run controls
- Idempotent run creation via `Idempotency-Key`
- Structured logging with request correlation (`X-Request-Id`)
- Prometheus metrics endpoint (`/metrics`)
- Build metadata endpoint (`/version`)

## 3) Quickstart

### Prerequisites
- Go 1.25+
- Docker

### Start Postgres and apply schema
```bash
make docker-up
make migrate
```

### Start API
```bash
export ADMIN_TOKEN=change-me-admin-token
export DATABASE_URL=postgres://durable:durable@localhost:5432/durable?sslmode=disable
export HTTP_ADDR=:8080
export ENV=dev
export LOG_LEVEL=info

go run ./cmd/api
```

### Create a tenant API key (admin)
In a new terminal:

```bash
curl -s -X POST http://localhost:8080/api-keys \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"demo-key","max_concurrent_runs":5,"max_requests_per_min":60}'
```

Example response:

```json
{
  "api_key_id": "9d4c4d22-5f1e-4e40-8ae0-cf0d3f65d6f7",
  "token": "sk_live_..."
}
```

Save both values:
- `API_KEY_ID=<api_key_id>`
- `API_TOKEN=<token>` (returned only once)

### Start worker (dedicated mode)
```bash
go run ./cmd/worker \
  --api-key-id="${API_KEY_ID}" \
  --poll-interval=250ms \
  --max-attempts=3 \
  --reclaim-after=5m \
  --retry-base-delay=2s \
  --default-step-timeout=30s
```

### Local stack with Docker Compose
Use `.env.example` as a starting point:

```bash
cp .env.example .env
```

Run Postgres + API:

```bash
docker compose up -d postgres api
```

Run worker (disabled by default, profile-gated):

```bash
docker compose --profile worker up -d worker
```

### GHCR images
Replace `<owner>` with your GitHub user/org.

```bash
docker pull ghcr.io/<owner>/agent-runtime-api:latest
docker pull ghcr.io/<owner>/agent-runtime-worker:latest
```

Run API container:

```bash
docker run --rm -p 8080:8080 \
  --env-file .env \
  ghcr.io/<owner>/agent-runtime-api:latest
```

Run worker container:

```bash
docker run --rm \
  --env-file .env \
  ghcr.io/<owner>/agent-runtime-worker:latest \
  --api-key-id="${API_KEY_ID}" \
  --poll-interval=250ms \
  --max-attempts=3 \
  --reclaim-after=5m
```

## 4) Authentication
There are two auth layers:

- Admin endpoints (`/api-keys`) require `Authorization: Bearer <ADMIN_TOKEN>`.
- Runtime endpoints (`/runs/*`) require `Authorization: Bearer <API_TOKEN>`.
- Public endpoints that do not require auth: `GET /healthz`, `GET /metrics`, `GET /version`.
- Authenticated runtime responses include `X-RateLimit-Limit` and `X-RateLimit-Remaining`.
- When request rate is exceeded, API returns `429` with `Retry-After`.

### Create API key via API (no manual DB step)
```bash
curl -s -X POST http://localhost:8080/api-keys \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"team-a","max_concurrent_runs":5,"max_requests_per_min":60}'
```

### List API keys
```bash
curl -s http://localhost:8080/api-keys \
  -H "Authorization: Bearer ${ADMIN_TOKEN}"
```

### Revoke API key
```bash
curl -i -X DELETE http://localhost:8080/api-keys/${API_KEY_ID} \
  -H "Authorization: Bearer ${ADMIN_TOKEN}"
```

### Runtime call with API token
```bash
curl -s http://localhost:8080/runs/${RUN_ID} \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Health check (no auth)
```bash
curl -s http://localhost:8080/healthz
```

### Build/version info (no auth)
```bash
curl -s http://localhost:8080/version
```

## 5) API Examples

### Create run (template + priority + webhook)
```bash
curl -s -X POST http://localhost:8080/runs \
  -H "Authorization: Bearer ${API_TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: run-demo-001" \
  -d '{
    "template_name": "default",
    "priority": 10,
    "webhook_url": "https://example.com/agent-callback"
  }'
```

Idempotency behavior:
- Repeating `POST /runs` with the same `Idempotency-Key` and same API key returns the same `run_id` (`200 OK`), not a duplicate run.

### Get run
```bash
curl -s http://localhost:8080/runs/${RUN_ID} \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### List steps
```bash
curl -s http://localhost:8080/runs/${RUN_ID}/steps \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Approve run
```bash
curl -s -X POST http://localhost:8080/runs/${RUN_ID}/approve \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Cancel run
```bash
curl -s -X POST http://localhost:8080/runs/${RUN_ID}/cancel \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Stream events (SSE)
```bash
curl -N http://localhost:8080/runs/${RUN_ID}/events \
  -H "Authorization: Bearer ${API_TOKEN}"
```

Resume from cursor:

```bash
curl -N "http://localhost:8080/runs/${RUN_ID}/events?since_id=42" \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Get cost
```bash
curl -s http://localhost:8080/runs/${RUN_ID}/cost \
  -H "Authorization: Bearer ${API_TOKEN}"
```

### Webhook signature notes
- On terminal run states (`SUCCEEDED`, `FAILED`), worker sends a webhook if `webhook_url` is configured.
- If `webhook_secret` exists on the run, worker adds:
  - `X-Signature: <hex(hmac_sha256(secret, body))>`

## 6) Worker Modes

### Shared workers
Operationally, you can run a shared fleet by running many worker processes and assigning each one a tenant `api_key_id` (for example via your orchestrator).

### Dedicated worker per `api_key_id` (current binary mode)
`cmd/worker` currently requires:
- `--api-key-id=<uuid>`

Optional tuning flags:
- `--poll-interval` (default `250ms`)
- `--max-attempts` (default `3`)
- `--reclaim-after` (default `5m`)
- `--retry-base-delay` (default `2s`)
- `--default-step-timeout` (default `30s`)

## 7) Templates

### Default template
On migration, a default template is seeded:
- `LLM`
- `TOOL`
- `APPROVAL`

### Custom templates
There is currently no public template-management API. Create templates directly in DB:

```sql
INSERT INTO workflow_templates (id, name)
VALUES (uuid_generate_v4(), 'ops-template');

INSERT INTO workflow_template_steps (id, template_id, position, name)
SELECT uuid_generate_v4(), wt.id, s.position, s.name
FROM workflow_templates wt
JOIN (VALUES
  (1, 'LLM'),
  (2, 'TOOL'),
  (3, 'APPROVAL')
) AS s(position, name) ON TRUE
WHERE wt.name = 'ops-template';
```

Then create a run with `"template_name": "ops-template"`.

## 8) Observability

### Logs
- Uses `log/slog` across API/worker/repository layers.
- Request middleware injects/propagates `X-Request-Id`.
- Request completion logs include method, path, status, duration, request id, and tenant id (when authenticated).

### Metrics
- `GET /metrics` exposes Prometheus metrics.
- Includes counters/histograms for run/step lifecycle and worker claim/execute performance.

## 9) Local Development

### Make targets
- `make test-setup` - download deps into local cache
- `make docker-build` - build local API + worker container images
- `make docker-up` - start Postgres (compose service)
- `make migrate` - apply SQL migrations
- `make fmt` - apply `gofmt -w` to all Go files
- `make fmt-check` - fail if any file is not gofmt-formatted
- `make vet` - run `go vet ./...`
- `make test` - run all tests
- `make test-unit` - run unit test suite (`go test ./...`)
- `make test-integration` - start Postgres (compose), migrate, run integration tests, then stop Postgres
- `make test-integration-db` - run integration tests against an already-running/migrated DB
- `make validate` - run formatting check, vet, unit tests, and integration tests
- `make lint` - alias for `make vet`
- `make docker-down` - stop Postgres

Run the full local validation pipeline:
```bash
make validate
```

### Example `.env`
Start from:

```bash
cp .env.example .env
```

Then adjust values:

```bash
HTTP_ADDR=:8080
DATABASE_URL=postgres://durable:durable@localhost:5432/durable?sslmode=disable
ENV=dev
LOG_LEVEL=info
ADMIN_TOKEN=change-me-admin-token
WORKER_API_KEY_ID=<tenant-api-key-id>
```

Load it locally:
```bash
set -a
source .env
set +a
```

### Configuration

| Variable | Default | Used by | Description |
|---|---|---|---|
| `HTTP_ADDR` | `:8080` | API | API bind address |
| `DATABASE_URL` | `postgres://durable:durable@localhost:5432/durable?sslmode=disable` | API + Worker | Postgres DSN |
| `ENV` | `dev` | API + Worker | Logger mode: `dev` (text+source) or `prod` (JSON) |
| `LOG_LEVEL` | `info` | API + Worker | Log level: `debug`, `info`, `warn`, `error` |
| `ADMIN_TOKEN` | empty | API | Bearer token for `/api-keys` admin endpoints |

## 10) Security Notes
- API tokens are generated as `sk_live_<32-random-bytes-hex>`.
- Raw API tokens are returned once on creation and never stored.
- Database stores only `SHA256` hash (`api_keys.token_hash`).
- Admin key operations are protected by `ADMIN_TOKEN`.
- Runtime APIs enforce tenant ownership (`api_key_id`) and return `404` on cross-tenant access.
- Request correlation via `X-Request-Id` supports audit/incident tracing.

## 11) Project Layout

```text
cmd/
  api/           # API server entrypoint
  cli/           # local utility commands (validate)
  worker/        # Worker entrypoint
internal/
  auth/          # auth context and tenant data
  config/        # env config
  domain/        # statuses and core types
  logging/       # slog logger factory
  repository/    # DB repositories (runs/steps/events/api keys)
  transport/http # router + middleware + handlers
  worker/        # claim/execute/retry/webhook engine
migrations/      # ordered SQL migrations
```

## 12) License
Licensed under Apache-2.0. See `LICENSE`.
