-- 0056: log saklama (retention) job'u icin indeks.
-- Retention job'u (server/retention) request_logs icin (tenant_id, ts)
-- indeksini (0013) kullanir; UDP istatistikleri icin dakika indeksi eklenir.
CREATE INDEX IF NOT EXISTS idx_udp_stats_minute_tenant ON udp_stats_minute (tenant_id, minute);
