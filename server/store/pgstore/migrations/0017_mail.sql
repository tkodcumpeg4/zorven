-- 0017: Webmail — token-kullanicisinin org-slug@mail.<domain> adresine gelen
-- ve gonderdigi e-postalarin Postgres'te saklandigi tablo.
--
-- Gelen mailler Go SMTP alicisi tarafindan (server/mail) parse edilip buraya
-- yazilir; giden mailler API uzerinden Postfix relay'ine (mail:587) teslim
-- edilmeden once burada kaydedilir. Adres->tenant eslesmesi tenant slug'i ile
-- yapilir (org-slug@mail.<domain>).

CREATE TABLE IF NOT EXISTS mail_messages (
    id           TEXT NOT NULL PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    direction    TEXT NOT NULL,              -- 'inbound' | 'outbound'
    from_addr    TEXT NOT NULL DEFAULT '',
    to_addr      TEXT NOT NULL DEFAULT '',
    subject      TEXT NOT NULL DEFAULT '',
    text_body    TEXT NOT NULL DEFAULT '',
    html_body    TEXT NOT NULL DEFAULT '',
    message_id   TEXT NOT NULL DEFAULT '',   -- Message-ID basligi
    in_reply_to  TEXT NOT NULL DEFAULT '',   -- In-Reply-To basligi
    seen         BOOLEAN NOT NULL DEFAULT false,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw          TEXT NOT NULL DEFAULT ''    -- ham RFC822 (debug/ilerideki ozellikler)
);

CREATE INDEX IF NOT EXISTS idx_mail_tenant_dir_time
    ON mail_messages (tenant_id, direction, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_mail_tenant_unseen
    ON mail_messages (tenant_id) WHERE seen = false AND direction = 'inbound';
