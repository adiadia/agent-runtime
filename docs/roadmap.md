# Roadmap

This roadmap is intentionally practical: stabilize core execution first, then increase developer experience and orchestration power.

## Phase 1: Core durability (done)
Status: completed baseline.

Delivered:
- Durable run/step/event persistence in Postgres
- Tenant isolation by `api_key_id`
- Retry scheduling with exponential backoff
- Idempotent run creation
- Approval gate support
- SSE event stream and terminal webhooks
- Cost tracking and per-tenant limits
- Structured logging and Prometheus metrics endpoint

## Phase 2: SDKs + developer experience
Status: next priority.

Must-have:
- Typed SDKs (Go/TypeScript/Python) for run lifecycle APIs
- Better API error model with stable machine-readable error codes
- Local dev tooling improvements (seed scripts, fixture templates)
- OpenAPI/Swagger generation + examples

Nice-to-have:
- CLI for run management and live event watching
- Hosted playground/demo UI
- Template validation helper utilities

## Phase 3: DAG + parallelism
Status: planned.

Must-have:
- DAG data model for non-linear step dependencies
- Parallel step execution with bounded concurrency
- Join/barrier semantics and deterministic completion criteria
- Backward-compatible mapping from ordered templates to DAG form

Nice-to-have:
- Conditional branching expressions
- Dynamic fan-out/fan-in patterns
- Step-level resource classes and scheduling hints

## Phase 4: Enterprise features
Status: planned.

Must-have:
- RBAC for admin and tenant operations
- Stronger audit log controls and export
- Data retention policies and archival strategies
- Compliance-focused operational controls (PII handling, key rotation)

Nice-to-have:
- SSO/SAML integration
- Advanced policy engine (per-tenant quotas, allowlists)
- Multi-region active-active deployment patterns

## Must-have vs nice-to-have summary

| Area | Must-have | Nice-to-have |
|---|---|---|
| SDK/DX | Official SDKs, error model, OpenAPI | CLI extras, hosted playground |
| Orchestration | DAG, parallelism, joins | conditional branches, fan-out/fan-in |
| Enterprise | RBAC, audit controls, retention | SSO integrations, advanced policy engine |

## Backward compatibility and migrations

Compatibility principles:
- Existing API endpoints should remain stable unless explicitly versioned.
- New fields should be additive and optional whenever possible.
- State transitions must remain monotonic and backward-safe.

Migration practices:
- Use ordered SQL migrations (`migrations/*.sql`) with `IF NOT EXISTS` guards where practical.
- Backfill data when introducing stricter constraints (for example token hashing migration).
- Prefer dual-read/dual-write periods for high-risk schema transitions.
- Test migrations against realistic data volumes before production rollout.

Rollout guidance:
1. Apply schema migration.
2. Deploy code that supports both old/new shape if needed.
3. Verify metrics and logs.
4. Remove deprecated fields in later release after safety window.
