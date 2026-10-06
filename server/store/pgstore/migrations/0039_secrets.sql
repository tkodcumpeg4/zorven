-- 0039_secrets.sql
-- FAZ 1 (F06): Secret Vault -- uygulama seviyesinde sifreli sirlar
CREATE TABLE IF NOT EXISTS secrets (
    id          text PRIMARY KEY,
    tenant_id   text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id  text NOT NULL,
    name        text NOT NULL,
    value_enc   bytea NOT NULL,           -- AES-256-GCM (nonce||ciphertext)
    key_version int  NOT NULL DEFAULT 1,  -- ileride key rotation icin
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_secrets_tenant  ON secrets (tenant_id);
CREATE INDEX IF NOT EXISTS idx_secrets_project ON secrets (project_id);
