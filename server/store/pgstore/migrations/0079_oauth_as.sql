-- 0079: OAuth 2.1 yetkilendirme sunucusu (MCP / Claude ozel baglayici).
-- Istemciler (dinamik kayit), kisa omurlu yetkilendirme kodlari, kullanici
-- yetkileri (grant) ve donen refresh token'lar. Sirlar ham saklanmaz: kod ve
-- refresh token icin yalnizca SHA-256 ozeti.

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id     text PRIMARY KEY,
    client_name   text NOT NULL DEFAULT '',
    redirect_uris jsonb NOT NULL DEFAULT '[]'::jsonb,
    grant_types   jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_ip    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oauth_grants (
    id           text PRIMARY KEY,
    user_id      text NOT NULL,
    tenant_id    text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    client_id    text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    resource     text NOT NULL,
    scope        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX IF NOT EXISTS idx_oauth_grants_user ON oauth_grants (user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_grants_tenant ON oauth_grants (tenant_id) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS oauth_codes (
    code_hash      text PRIMARY KEY,
    client_id      text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id        text NOT NULL,
    tenant_id      text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    redirect_uri   text NOT NULL,
    scope          text NOT NULL,
    resource       text NOT NULL,
    code_challenge text NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz
);
CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires ON oauth_codes (expires_at);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    token_hash text PRIMARY KEY,
    grant_id   text NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_grant ON oauth_refresh_tokens (grant_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_expires ON oauth_refresh_tokens (expires_at);
