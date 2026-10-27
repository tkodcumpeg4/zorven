-- 0082: MCP cagri logu. Yalnizca ust veri: yontem, arac adi, durum, sure. Arguman
-- ve sonuc govdesi ASLA saklanmaz.
CREATE TABLE IF NOT EXISTS mcp_calls (
    id          bigserial PRIMARY KEY,
    tenant_id   text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tunnel_id   text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    ts          timestamptz NOT NULL DEFAULT now(),
    session     text NOT NULL DEFAULT '',
    client      text NOT NULL DEFAULT '',
    principal   text NOT NULL DEFAULT '',
    method      text NOT NULL DEFAULT '',
    tool        text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'ok',
    error_code  integer NOT NULL DEFAULT 0,
    duration_ms integer NOT NULL DEFAULT 0,
    bytes_in    bigint NOT NULL DEFAULT 0,
    bytes_out   bigint NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mcp_calls_tunnel_ts ON mcp_calls (tunnel_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_mcp_calls_tenant_ts ON mcp_calls (tenant_id, ts);
