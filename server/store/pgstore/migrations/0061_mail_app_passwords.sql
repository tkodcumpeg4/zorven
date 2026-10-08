-- 0060: Mail istemcileri (IMAP/SMTP) icin uygulama parolalari.
--
-- Giris adi tam e-posta adresidir (slug@mail.<domain> veya info@<platform>).
-- Parola yalnizca olusturulurken bir kez gosterilir; burada argon2id hash'i saklanir.

CREATE TABLE IF NOT EXISTS mail_app_passwords (
    id            TEXT        NOT NULL PRIMARY KEY,
    tenant_id     TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mailbox       TEXT        NOT NULL,
    label         TEXT        NOT NULL DEFAULT '',
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ,
    last_used_ip  TEXT        NOT NULL DEFAULT '',
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mail_app_pw_mailbox
    ON mail_app_passwords (mailbox) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mail_app_pw_tenant
    ON mail_app_passwords (tenant_id);
