-- 0059: Webmail IMAP erisimi — mesaj basina klasor, posta kutusu adresi, kalici UID
-- ve ek bayraklar. Mail istemcileri (Gmail uygulamasi, Outlook, Apple Mail,
-- Thunderbird) IMAP ile baglanir; her klasorde UID'ler kalici ve artan olmalidir.
--
--   folder  : inbox | sent | trash | drafts  ('' = eski satir, asagida doldurulur)
--   mailbox : mesajin ait oldugu kutu adresi (kucuk harf): gelende alici, gidende gonderen
--   uid     : (tenant, mailbox, folder) icinde 1'den artan kalici UID
--
-- mail_uid_state, klasor basina son verilen UID'yi ve UIDVALIDITY'yi tutar.

ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS folder   TEXT    NOT NULL DEFAULT '';
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS mailbox  TEXT    NOT NULL DEFAULT '';
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS uid      BIGINT  NOT NULL DEFAULT 0;
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS flagged  BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS answered BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS deleted  BOOLEAN NOT NULL DEFAULT false;

UPDATE mail_messages
   SET folder  = CASE direction WHEN 'inbound' THEN 'inbox' ELSE 'sent' END,
       mailbox = lower(btrim(CASE direction WHEN 'inbound' THEN to_addr ELSE from_addr END))
 WHERE folder = '';

-- Mevcut mesajlara received_at sirasiyla (esitlikte id) UID ata.
UPDATE mail_messages m
   SET uid = s.rn
  FROM (SELECT id,
               row_number() OVER (PARTITION BY tenant_id, mailbox, folder
                                  ORDER BY received_at, id) AS rn
          FROM mail_messages
         WHERE uid = 0) s
 WHERE m.id = s.id;

CREATE TABLE IF NOT EXISTS mail_uid_state (
    tenant_id    TEXT   NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mailbox      TEXT   NOT NULL,
    folder       TEXT   NOT NULL,
    last_uid     BIGINT NOT NULL DEFAULT 0,
    uid_validity BIGINT NOT NULL DEFAULT (extract(epoch FROM now())::bigint),
    PRIMARY KEY (tenant_id, mailbox, folder)
);

INSERT INTO mail_uid_state (tenant_id, mailbox, folder, last_uid)
SELECT tenant_id, mailbox, folder, max(uid)
  FROM mail_messages
 GROUP BY tenant_id, mailbox, folder
ON CONFLICT (tenant_id, mailbox, folder) DO NOTHING;

CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_mailbox_folder_uid
    ON mail_messages (tenant_id, mailbox, folder, uid);

CREATE INDEX IF NOT EXISTS idx_mail_mailbox_msgid
    ON mail_messages (tenant_id, mailbox, folder, message_id)
    WHERE message_id <> '';
