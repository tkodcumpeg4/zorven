-- Guncellemeler toplu e-postasi: alici basina gonderim gunlugu.
-- Surec yeniden baslasa bile yalniz gonderilmemisler icin devam edilebilir.
CREATE TABLE IF NOT EXISTS updates_email_deliveries (
    post_id    TEXT NOT NULL REFERENCES updates_posts(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('sent', 'failed')),
    attempts   INTEGER NOT NULL DEFAULT 1,
    error      TEXT NOT NULL DEFAULT '',
    sent_at    TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, user_id)
);
CREATE INDEX IF NOT EXISTS updates_email_deliveries_status_idx ON updates_email_deliveries (post_id, status);

-- Duyuru e-postasi OPT-IN: tercih satiri yok = abone DEGIL. Satir "soruldu" demektir
-- (subscribed=false: reddetti / abonelikten cikti). consented_at = acik riza zamani,
-- source = riza kaynagi (signup | profile | banner | unsub).
ALTER TABLE announcement_prefs ADD COLUMN IF NOT EXISTS consented_at TIMESTAMPTZ;
ALTER TABLE announcement_prefs ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';
-- Onceki surumde acikca abone kalan satirlar (subscribed=true) riza sayilir.
UPDATE announcement_prefs SET consented_at = COALESCE(updated_at, now()), source = 'profile'
  WHERE subscribed = true AND consented_at IS NULL;
