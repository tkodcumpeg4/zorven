-- 0078: ham TCP tunel metrikleri
--
-- tcp_stats_minute: dakikalik TCP ozeti (udp_stats_minute'in TCP karsiligi).
-- Ortalama sure saklanmaz; dur_sum_ms/dur_count tutulur ki ayni dakikanin
-- birden fazla yazimi dogru agirlikla birlesebilsin (ortalama = sum/count).

CREATE TABLE IF NOT EXISTS tcp_stats_minute (
    tunnel_id        text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id        text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    minute           timestamptz NOT NULL,
    conns_new        bigint NOT NULL DEFAULT 0,
    conns_peak       int    NOT NULL DEFAULT 0,
    conns_rejected   bigint NOT NULL DEFAULT 0,
    rejected_ip      bigint NOT NULL DEFAULT 0,
    rejected_door    bigint NOT NULL DEFAULT 0,
    rejected_offline bigint NOT NULL DEFAULT 0,
    rejected_dial    bigint NOT NULL DEFAULT 0,
    bytes_in         bigint NOT NULL DEFAULT 0,
    bytes_out        bigint NOT NULL DEFAULT 0,
    dur_sum_ms       bigint NOT NULL DEFAULT 0,
    dur_count        bigint NOT NULL DEFAULT 0,
    dur_max_ms       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tunnel_id, minute)
);
CREATE INDEX IF NOT EXISTS idx_tcp_stats_minute_minute ON tcp_stats_minute(minute);
CREATE INDEX IF NOT EXISTS idx_tcp_stats_minute_tenant ON tcp_stats_minute(tenant_id, minute);
