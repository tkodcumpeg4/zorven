-- 0059: web ile kapi acma (web door) - ham TCP/UDP tunelleri icin.
--
-- Ziyaretci, tunelin kapi adresinde (door--<ad>--<kiraci>.<platform>) Basic/OAuth
-- ile giris yapar; basarida kendi IP'si icin sureli bir "grant" olusur ve ham
-- port/SNI baglantisi bu IP'den kabul edilir. Parola/jeton ASLA saklanmaz.

CREATE TABLE IF NOT EXISTS tunnel_doors (
    tunnel_id    VARCHAR(32) PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id    text        NOT NULL DEFAULT '',
    enabled      boolean     NOT NULL DEFAULT false,
    duration_sec integer     NOT NULL DEFAULT 43200,   -- 3600 | 43200 | 86400 | 604800
    host         text        NOT NULL DEFAULT '',      -- kapi hostname'i (kucuk harf)
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tunnel_doors_host ON tunnel_doors (host) WHERE host <> '';
CREATE INDEX IF NOT EXISTS idx_tunnel_doors_tenant ON tunnel_doors (tenant_id);

CREATE TABLE IF NOT EXISTS tunnel_door_grants (
    id          text        PRIMARY KEY,
    tenant_id   text        NOT NULL DEFAULT '',
    tunnel_id   VARCHAR(32) NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    ip          text        NOT NULL,
    identity    text        NOT NULL DEFAULT '',        -- kullanici adi veya e-posta
    method      text        NOT NULL DEFAULT '',        -- basic | oauth
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_tdg_tunnel_ip ON tunnel_door_grants (tunnel_id, ip, expires_at);
CREATE INDEX IF NOT EXISTS idx_tdg_tenant_tunnel ON tunnel_door_grants (tenant_id, tunnel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tdg_expires ON tunnel_door_grants (expires_at);

-- Toplulastirilmis olaylar (ornegin dakikalik "blocked_no_grant") bir satirda n olay tasir.
ALTER TABLE tunnel_access_events ADD COLUMN IF NOT EXISTS count integer NOT NULL DEFAULT 1;
