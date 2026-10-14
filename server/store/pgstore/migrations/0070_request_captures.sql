-- 0070: Kalici istek/yanit govdesi yakalama (inspector / replay).
-- id, request_logs.id ile eslesir (FK yok: yazim asenkron, kiraci silmede
-- elle temizlenir; retention sahipsizleri de siler). Baslik/meta/govde
-- uygulama katmaninda gzip + AES-256-GCM ile sifrelenir (ZORVEN_SECRET_KEY'den
-- turetilmis anahtar); jsonb kolonlari sifreli blogu base64 JSON metni olarak
-- tutar. Migration tablespace'e bagli degildir: ZORVEN_CAPTURE_TABLESPACE
-- tanimliysa sunucu acilista tabloyu ve indekslerini oraya tasir.

CREATE TABLE IF NOT EXISTS request_captures (
    id              text PRIMARY KEY,
    tenant_id       text NOT NULL,
    tunnel_id       text NOT NULL DEFAULT '',
    ts              timestamptz NOT NULL,
    meta            jsonb,
    req_headers     jsonb,
    resp_headers    jsonb,
    req_body        bytea,
    resp_body       bytea,
    enc_key_version int NOT NULL DEFAULT 1,
    compressed      boolean NOT NULL DEFAULT false,
    req_truncated   boolean NOT NULL DEFAULT false,
    resp_truncated  boolean NOT NULL DEFAULT false,
    req_size        int NOT NULL DEFAULT 0,
    resp_size       int NOT NULL DEFAULT 0,
    stored_bytes    bigint NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_request_captures_tenant_ts ON request_captures (tenant_id, ts);
