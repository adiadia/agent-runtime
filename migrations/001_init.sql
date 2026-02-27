CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    max_concurrent_runs INT NOT NULL DEFAULT 5,
    max_requests_per_min INT NOT NULL DEFAULT 60,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS runs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    api_key_id UUID NOT NULL REFERENCES api_keys(id),
    status TEXT NOT NULL,
    current_step TEXT,
    priority INT NOT NULL DEFAULT 0,
    webhook_url TEXT,
    webhook_secret TEXT,
    total_cost_usd NUMERIC(10,6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    timeout_seconds INTEGER,
    next_run_at TIMESTAMP,
    cost_usd NUMERIC(10,6) NOT NULL DEFAULT 0,
    input JSONB,
    output JSONB,
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS events (
    seq BIGSERIAL UNIQUE,
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id UUID,
    type TEXT NOT NULL,
    payload JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS run_requests (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (api_key_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS workflow_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workflow_template_steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    template_id UUID NOT NULL REFERENCES workflow_templates(id) ON DELETE CASCADE,
    position INT NOT NULL,
    name TEXT NOT NULL,
    timeout_seconds INTEGER,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, position)
);

INSERT INTO workflow_templates (id, name)
VALUES (uuid_generate_v4(), 'default')
ON CONFLICT (name) DO NOTHING;

INSERT INTO workflow_template_steps (id, template_id, position, name)
SELECT
    uuid_generate_v4(),
    wt.id,
    seed.position,
    seed.name
FROM workflow_templates wt
JOIN (
    VALUES
        (1, 'LLM'),
        (2, 'TOOL'),
        (3, 'APPROVAL')
) AS seed(position, name) ON TRUE
WHERE wt.name = 'default'
ON CONFLICT (template_id, position) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_steps_run_id ON steps(run_id);
CREATE INDEX IF NOT EXISTS idx_events_run_id ON events(run_id);
CREATE INDEX IF NOT EXISTS idx_runs_api_key_id ON runs(api_key_id);
CREATE INDEX IF NOT EXISTS idx_run_requests_api_key_id ON run_requests(api_key_id);
CREATE INDEX IF NOT EXISTS idx_workflow_template_steps_template_id ON workflow_template_steps(template_id);
