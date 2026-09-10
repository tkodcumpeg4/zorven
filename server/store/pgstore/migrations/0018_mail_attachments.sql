-- 0018: Webmail ek dosyalari (attachments).
--
-- Gelen maillerdeki ekler SMTP alicisi tarafindan parse edilip buraya yazilir;
-- giden maillerde kullanici yukledigi ekler multipart/mixed olarak gonderilmeden
-- once burada saklanir. Icerik Postgres BYTEA'da tutulur (DB yedegine dahil).

CREATE TABLE IF NOT EXISTS mail_attachments (
    id           TEXT NOT NULL PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL DEFAULT 'dosya',
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    content      BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mail_attach_msg ON mail_attachments (message_id);
CREATE INDEX IF NOT EXISTS idx_mail_attach_tenant ON mail_attachments (tenant_id);
