CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS name TEXT;

UPDATE api_keys
SET name = 'key-' || substring(id::text FROM 1 FOR 8)
WHERE name IS NULL OR btrim(name) = '';

ALTER TABLE api_keys
    ALTER COLUMN name SET NOT NULL;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS token_hash TEXT;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'api_keys'
          AND column_name = 'token'
    ) THEN
        EXECUTE $q$
            UPDATE api_keys
            SET token_hash = encode(digest(token, 'sha256'), 'hex')
            WHERE token_hash IS NULL
        $q$;
    END IF;
END;
$$;

ALTER TABLE api_keys
    ALTER COLUMN token_hash SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_token_hash_unique
    ON api_keys(token_hash);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'api_keys'
          AND column_name = 'token'
    ) THEN
        EXECUTE 'ALTER TABLE api_keys DROP COLUMN token';
    END IF;
END;
$$;
