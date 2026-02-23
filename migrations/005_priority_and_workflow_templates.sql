ALTER TABLE runs
    ADD COLUMN IF NOT EXISTS priority INT NOT NULL DEFAULT 0;

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

CREATE INDEX IF NOT EXISTS idx_workflow_template_steps_template_id
    ON workflow_template_steps(template_id);

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
