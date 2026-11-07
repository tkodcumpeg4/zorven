-- Duyuru yazilarina Cince (zh) baslik/metin alanlari.
ALTER TABLE updates_posts ADD COLUMN IF NOT EXISTS title_zh TEXT NOT NULL DEFAULT '';
ALTER TABLE updates_posts ADD COLUMN IF NOT EXISTS body_zh TEXT NOT NULL DEFAULT '';
