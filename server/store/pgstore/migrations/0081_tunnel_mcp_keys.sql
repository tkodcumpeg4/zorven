-- 0081: MCP tunelleri icin API anahtarlari (auth=apikey). Ham anahtar ASLA
-- saklanmaz; yalnizca SHA-256 ozeti ve gorunur on-ek.
CREATE TABLE IF NOT EXISTS tunnel_mcp_keys (
    id         text PRIMARY KEY,
    tunnel_id  text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label      text NOT NULL DEFAULT '',
    prefix     text NOT NULL DEFAULT '',
    key_hash   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_tunnel_mcp_keys_tunnel ON tunnel_mcp_keys (tunnel_id);
