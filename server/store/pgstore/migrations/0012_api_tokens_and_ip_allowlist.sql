-- 0012_api_tokens_and_ip_allowlist.sql
-- Programatik REST API Tokenlari ve Ingress IP Izin Listesi (IP Allowlist)

-- 1. Programatik REST API Tokenlari
CREATE TABLE IF NOT EXISTS api_tokens (
    id           text PRIMARY KEY,
    tenant_id    text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      text REFERENCES "user"("id") ON DELETE SET NULL,
    name         text NOT NULL,
    token_id     text NOT NULL UNIQUE,
    token_hash   text NOT NULL,
    token_prefix text NOT NULL DEFAULT 'zrv_api_',
    scopes       text[] NOT NULL DEFAULT '{"read", "write"}',
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_lookup ON api_tokens(token_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_tokens_tenant ON api_tokens(tenant_id);

-- 2. Ingress IP Izin Listesi (CIDR bazli filtreleme)
CREATE TABLE IF NOT EXISTS ip_allowlist_rules (
    id          text PRIMARY KEY,
    tenant_id   text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tunnel_id   text REFERENCES tunnels(id) ON DELETE CASCADE,
    cidr        text NOT NULL,
    description text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ip_rules_tenant_tunnel ON ip_allowlist_rules(tenant_id, tunnel_id, enabled);
CREATE INDEX IF NOT EXISTS idx_ip_rules_tunnel ON ip_allowlist_rules(tunnel_id);
