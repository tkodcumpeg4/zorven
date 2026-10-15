-- Guncellemeler (surum / guncelleme / bakim duyurulari) ve urun duyurusu e-posta tercihi.
-- Icerik Platform admin panelinden yazilir; TR zorunlu, EN/JA istege bagli.
CREATE TABLE IF NOT EXISTS updates_posts (
    id                TEXT PRIMARY KEY,
    slug              TEXT NOT NULL,
    kind              TEXT NOT NULL DEFAULT 'update' CHECK (kind IN ('release', 'update', 'maintenance')),
    version           TEXT NOT NULL DEFAULT '',
    title_tr          TEXT NOT NULL,
    body_tr           TEXT NOT NULL DEFAULT '',
    title_en          TEXT NOT NULL DEFAULT '',
    body_en           TEXT NOT NULL DEFAULT '',
    title_ja          TEXT NOT NULL DEFAULT '',
    body_ja           TEXT NOT NULL DEFAULT '',
    maintenance_start TIMESTAMPTZ,
    maintenance_end   TIMESTAMPTZ,
    published_at      TIMESTAMPTZ,            -- NULL = taslak
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    emailed_at        TIMESTAMPTZ,            -- toplu e-posta kuyruga alindiginda
    email_stats       JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE UNIQUE INDEX IF NOT EXISTS updates_posts_slug_uq ON updates_posts (lower(slug));
CREATE INDEX IF NOT EXISTS updates_posts_published_idx ON updates_posts (published_at DESC) WHERE published_at IS NOT NULL;

-- Kullanici basina duyuru tercihi. SATIR YOKSA = abone (varsayilan acik).
-- Abonelikten cikma jetonu durumsuzdur (HMAC(user_id)); satir tutmaz.
-- lang: kullanicinin panelde sectigi dil (tr/en/ja); e-postanin dilini belirler.
CREATE TABLE IF NOT EXISTS announcement_prefs (
    user_id         TEXT PRIMARY KEY,
    subscribed      BOOLEAN NOT NULL DEFAULT true,
    unsubscribed_at TIMESTAMPTZ,
    lang            TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
