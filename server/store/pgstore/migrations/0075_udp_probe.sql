-- 0074: UDP tunel olcumu (FeatureUDPProbe)
--
-- Dakikalik UDP ozetine gecikme (p50/p95, ms), yoklama kayip yuzdesi ve kuyruk
-- tasmasiyla dusen datagram sayisi eklenir. NULL = o dakikada olcum yok
-- (istemci desteklemiyor ya da yanki gelmedi).

ALTER TABLE udp_stats_minute ADD COLUMN IF NOT EXISTS rtt_p50_ms double precision;
ALTER TABLE udp_stats_minute ADD COLUMN IF NOT EXISTS rtt_p95_ms double precision;
ALTER TABLE udp_stats_minute ADD COLUMN IF NOT EXISTS probe_loss_pct double precision;
ALTER TABLE udp_stats_minute ADD COLUMN IF NOT EXISTS dropped_datagrams bigint NOT NULL DEFAULT 0;
