-- 0050: UDP ileri — FAZ 4 / F24
--
-- tunnel_udp: tunel basina UDP sinirlari. Kayit yoksa guvenli varsayilanlar
-- (store.DefaultTunnelUDP) uygulanir; herkeste flow ust siniri vardir.
-- udp_stats_minute: dakikalik UDP istatistik ozeti (request_logs'un UDP karsiligi;
-- datagram basina satir YAZILMAZ, sunucu bellekte toplayip dakikada bir yazar).

CREATE TABLE IF NOT EXISTS tunnel_udp (
    tunnel_id        text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id        text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    idle_timeout_sec int NOT NULL DEFAULT 90,
    max_packet_bytes int NOT NULL DEFAULT 65507,
    max_pps          int NOT NULL DEFAULT 0,    -- tunel geneli paket/sn (0 = sinirsiz)
    max_flow_pps     int NOT NULL DEFAULT 0,    -- flow basina paket/sn (0 = sinirsiz)
    max_flows        int NOT NULL DEFAULT 1024, -- es zamanli flow (kaynak adres) siniri
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tunnel_udp_tenant ON tunnel_udp(tenant_id);

CREATE TABLE IF NOT EXISTS udp_stats_minute (
    tunnel_id     text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id     text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    minute        timestamptz NOT NULL,
    packets_in    bigint NOT NULL DEFAULT 0,
    packets_out   bigint NOT NULL DEFAULT 0,
    bytes_in      bigint NOT NULL DEFAULT 0,
    bytes_out     bigint NOT NULL DEFAULT 0,
    flows_new     bigint NOT NULL DEFAULT 0,
    flows_peak    int    NOT NULL DEFAULT 0,
    dropped_rate  bigint NOT NULL DEFAULT 0,
    dropped_size  bigint NOT NULL DEFAULT 0,
    dropped_flows bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tunnel_id, minute)
);
CREATE INDEX IF NOT EXISTS idx_udp_stats_minute_minute ON udp_stats_minute(minute);
