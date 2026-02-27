# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.1.3] - 2026-02-27

### Added
- Release automation for publishing multi-arch Docker images to GHCR.
- Project-level validation command (`go run ./cmd/cli validate`) and CI docs checks.
- Embedded schema bootstrap at API/worker startup with advisory-lock migration coordination.
- Integration coverage for startup against an empty database (`EnsureSchema` bootstrap path).

### Changed
- Expanded container support with dedicated API and worker production Dockerfiles.
- `/healthz` now returns `503` when required schema is missing instead of reporting ready.
- `POST /runs` priority contract is documented and tested as JSON integer-only.

## [v0.1.2] - 2026-02-24

## [v0.1.1] - 2026-02-24

### Fixed
- Approve API now enforces WAITING_APPROVAL semantics and returns a clear conflict error for invalid approval attempts.
- Approve API is idempotent for already-approved runs/steps.
- Added integration and router coverage for approval edge cases and HTTP status mapping.

### Changed
- Updated Apache-2.0 LICENSE attribution to: `Copyright (c) 2026 Aditya Tiwari`.

## [v0.1.0] - 2026-02-23

### Added
- Durable run/step orchestration backed by Postgres.
- Multi-tenant API key model with token hashing and admin-protected key lifecycle endpoints.
- Dedicated worker mode per `api_key_id` with tenant-scoped claiming.
- Retry scheduling with exponential backoff and per-step timeout support.
- Approval workflow support with explicit waiting/approve transitions.
- Idempotent run creation with `Idempotency-Key`.
- Server-Sent Events endpoint for streaming run events (`GET /runs/{id}/events`).
- Webhook callbacks for terminal run states with optional HMAC `X-Signature`.
- Per-step and per-run cost tracking with `GET /runs/{id}/cost`.
- Priority-aware claiming and workflow template-based step planning.
- Per-api-key rate limits and concurrent run limits.
- Prometheus metrics endpoint (`GET /metrics`) and structured logging with request IDs.
- Apache-2.0 licensing (`LICENSE`, `NOTICE`) and SPDX headers in Go source files.
