-- 0013_request_logs.sql
-- Kalici istek loglari (web + masaustu "Loglar" ekrani, gelismis filtreler).
-- Bellek ici halka tampon (reqlog.Ring) canli akis icin kalir; bu tablo
-- gecmis/filtreli sorgular icindir.

CREATE TABLE IF NOT EXISTS request_logs (
    id          text PRIMARY KEY,
    tenant_id   text NOT NULL DEFAULT '',
    tunnel_id   text NOT NULL DEFAULT '',
    hostname    text NOT NULL DEFAULT '',
    client_ip   text NOT NULL DEFAULT '',
    ts          timestamptz NOT NULL,
    method      text NOT NULL DEFAULT '',
    path        text NOT NULL DEFAULT '',
    status      int  NOT NULL DEFAULT 0,
    duration_ms bigint NOT NULL DEFAULT 0,
    bytes_in    bigint NOT NULL DEFAULT 0,
    bytes_out   bigint NOT NULL DEFAULT 0
);

-- Sik sorgu desenleri: kiraci + zaman (DESC), tunel + zaman, status.
CREATE INDEX IF NOT EXISTS idx_request_logs_tenant_ts ON request_logs (tenant_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_tunnel_ts ON request_logs (tunnel_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_status    ON request_logs (tenant_id, status);
