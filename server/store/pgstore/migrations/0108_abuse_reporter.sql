-- Kotuye kullanim bildiriminde bildirenin kimligi (spam onlemi ve inceleme icin):
-- kategori, tarayici bilgisi, kalici bildiren kimligi (cerez), TLS parmak izi, dil.
ALTER TABLE abuse_reports ADD COLUMN IF NOT EXISTS category      TEXT NOT NULL DEFAULT '';
ALTER TABLE abuse_reports ADD COLUMN IF NOT EXISTS reporter_ua   TEXT NOT NULL DEFAULT '';
ALTER TABLE abuse_reports ADD COLUMN IF NOT EXISTS reporter_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE abuse_reports ADD COLUMN IF NOT EXISTS reporter_tls  TEXT NOT NULL DEFAULT '';
ALTER TABLE abuse_reports ADD COLUMN IF NOT EXISTS reporter_lang TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_abuse_reports_ip_time ON abuse_reports (reporter_ip, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_abuse_reports_rid_time ON abuse_reports (reporter_id, created_at DESC) WHERE reporter_id <> '';
