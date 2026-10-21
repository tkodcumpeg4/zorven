-- 0077: Zorven Net (WireGuard Mod A) kontrol duzlemi.
--
-- wg_nodes: cihaz dugumleri (kalici /32 ic IP + acik anahtar). Oturum
-- dugumleri (zorven access) bellekte yasar, burada saklanmaz.
-- wg_shares: bir cihazin yerel portunun paylasimi + alicilar (kullanici/grup/cihaz).

CREATE TABLE IF NOT EXISTS wg_nodes (
    id           text PRIMARY KEY,
    tenant_id    text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind         text NOT NULL DEFAULT 'device' CHECK (kind IN ('device', 'session')),
    client_id    text,
    name         text NOT NULL,
    ip           inet NOT NULL,
    wg_pub       text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wg_nodes_pub ON wg_nodes (wg_pub);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wg_nodes_ip ON wg_nodes (tenant_id, ip);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wg_nodes_client ON wg_nodes (client_id) WHERE client_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS wg_shares (
    id              text PRIMARY KEY,
    tenant_id       text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_client_id text NOT NULL,
    name            text NOT NULL,
    proto           text NOT NULL CHECK (proto IN ('tcp', 'udp')),
    port            int  NOT NULL CHECK (port BETWEEN 1 AND 65535),
    grantees        jsonb NOT NULL DEFAULT '[]'::jsonb,
    ephemeral       boolean NOT NULL DEFAULT false,
    expires_at      timestamptz,
    created_by      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_wg_shares_tenant ON wg_shares (tenant_id);
