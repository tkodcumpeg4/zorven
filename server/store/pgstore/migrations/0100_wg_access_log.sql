-- 0100: Zorven Net Mod B erisim gunlugu (Z5).
--
-- Hedef Mod B dugumunun gelen akis OZETLERI (kaynak, proto, port, karar, sayaclar).
-- ICERIK YOK. Saklama 30 gun (netctl.AccessRetention; Manager.Run budar).
-- Politika ONIZLEMESI icin yeni tablo gerekmez: Grant.SourceTags / NetProto /
-- NetPorts mevcut resource_policies.grants jsonb alaninda tasinir.

CREATE TABLE IF NOT EXISTS wg_access_events (
    id            bigserial PRIMARY KEY,
    tenant_id     text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    at            timestamptz NOT NULL DEFAULT now(),
    src_ip        text NOT NULL,
    src_name      text NOT NULL DEFAULT '',
    src_client_id text NOT NULL DEFAULT '',
    dst_ip        text NOT NULL,
    dst_name      text NOT NULL DEFAULT '',
    dst_client_id text NOT NULL DEFAULT '',
    proto         text NOT NULL,
    port          integer NOT NULL DEFAULT 0,
    decision      text NOT NULL CHECK (decision IN ('allow', 'deny')),
    rule          text NOT NULL DEFAULT '',
    resource      text NOT NULL DEFAULT '',
    flows         integer NOT NULL DEFAULT 0,
    packets       bigint NOT NULL DEFAULT 0,
    bytes         bigint NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_wg_access_tenant_at ON wg_access_events (tenant_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_wg_access_at ON wg_access_events (at);
