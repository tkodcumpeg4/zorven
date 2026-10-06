-- 0040_policies.sql
-- FAZ 1 (F04): Birlesik Policy motoru
CREATE TABLE IF NOT EXISTS policies (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id text NOT NULL,
    name       text NOT NULL,
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,  -- { "rules": [ {match, action} ] }
    enabled    boolean NOT NULL DEFAULT true,
    priority   int NOT NULL DEFAULT 100,            -- kucuk once uygulanir
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_policies_tenant  ON policies (tenant_id);
CREATE INDEX IF NOT EXISTS idx_policies_project ON policies (project_id);

CREATE TABLE IF NOT EXISTS policy_bindings (
    policy_id  text NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    tunnel_id  text NOT NULL DEFAULT '',   -- ya tunnel_id ya hostname dolu (biri)
    hostname   text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (policy_id, tunnel_id, hostname)
);
CREATE INDEX IF NOT EXISTS idx_policy_bindings_tunnel   ON policy_bindings (tunnel_id);
CREATE INDEX IF NOT EXISTS idx_policy_bindings_hostname ON policy_bindings (hostname);
