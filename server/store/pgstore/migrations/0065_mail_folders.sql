-- 0064: Webmail IMAP kullanici klasorleri. Sistem klasorleri (INBOX, Sent, Trash,
-- Drafts) sanaldir ve burada satiri yoktur; yalnizca kullanicinin olusturdugu
-- klasorler saklanir. Mesajlar mail_messages.folder = 'u:' || name ile bu
-- klasorlere baglanir; UID/UIDVALIDITY mail_uid_state'te ayni anahtarla tutulur.
-- name: IMAP adi, '/' ayiracli (ic ice klasor). special_use: \Archive, \Junk vb.

CREATE TABLE IF NOT EXISTS mail_folders (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mailbox     TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    special_use TEXT,
    subscribed  BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, mailbox, name)
);

-- Buyuk/kucuk harf farkiyla ayri klasor olusmasin (macOS/Windows istemcileri karisir).
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_folders_lower
    ON mail_folders (tenant_id, mailbox, lower(name));
