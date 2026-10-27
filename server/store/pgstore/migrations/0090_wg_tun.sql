-- 0090: Zorven Net Mod B (OS TUN, W3).
--
-- wg_networks: kiraci basina ic adres havuzu (CIDR). Satir yoksa varsayilan 100.64.0.0/10.
-- wg_tun_rules: cihazdan cihaza izin kurallari (default deny). Kaynak device|group(everyone),
-- hedef cihaz kimligi veya "*". Mod B dugumu cihaz kimligiyle girer (R8).

CREATE TABLE IF NOT EXISTS wg_networks (
    tenant_id  text PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    cidr       cidr NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wg_tun_rules (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL DEFAULT '',
    src_type   text NOT NULL CHECK (src_type IN ('device', 'group')),
    src_id     text NOT NULL,
    dst        text NOT NULL DEFAULT '*',
    proto      text NOT NULL DEFAULT 'any' CHECK (proto IN ('tcp', 'udp', 'icmp', 'any')),
    ports      text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_wg_tun_rules_tenant ON wg_tun_rules (tenant_id);
