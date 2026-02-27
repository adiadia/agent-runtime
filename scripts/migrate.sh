#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
SERVICE="${POSTGRES_SERVICE:-postgres}"
DB_USER="${POSTGRES_USER:-durable}"
DB_NAME="${POSTGRES_DB:-durable}"

echo "[migrate] waiting for postgres service '${SERVICE}' to be ready..."
for i in $(seq 1 30); do
  if docker compose -f "${COMPOSE_FILE}" exec -T "${SERVICE}" pg_isready -U "${DB_USER}" -d "${DB_NAME}" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "[migrate] applying migrations in order..."
for file in $(ls migrations/*.sql | sort); do
  echo "[migrate] applying ${file}"
  cat "${file}" | docker compose -f "${COMPOSE_FILE}" exec -T "${SERVICE}" psql -v ON_ERROR_STOP=1 -U "${DB_USER}" -d "${DB_NAME}"
done

echo "[migrate] done"
