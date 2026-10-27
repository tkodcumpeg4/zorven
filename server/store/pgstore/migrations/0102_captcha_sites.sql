-- 0102: Zorven CAPTCHA siteleri ve gunluk sayaclari.
--
-- secret_hash: gizli anahtarin (zcsec_...) SHA-256 onaltilik ozeti; sirrin kendisi
-- saklanmaz, yalniz olusturma/yenilemede bir kez gosterilir.
-- domains: bos dizi = her alan adi. difficulty: PoW ilk sifir bit sayisi (14-22).
-- captcha_counters.site_id, platform sitesi icin 'platform' degerini de alir (DB'de
-- satiri olmayan yerlesik site); bu yuzden captcha_sites'a yabanci anahtar YOKTUR.

CREATE TABLE IF NOT EXISTS captcha_sites (
    id           text PRIMARY KEY,
    tenant_id    text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         text NOT NULL,
    site_key     text NOT NULL UNIQUE,
    secret_hash  text NOT NULL UNIQUE,
    domains      text[] NOT NULL DEFAULT '{}',
    difficulty   integer NOT NULL DEFAULT 17 CHECK (difficulty BETWEEN 14 AND 22),
    -- Dogrulama sonrasi birinci taraf gecis cerezi (zcap_pass); varsayilan kapali.
    pass_enabled      boolean NOT NULL DEFAULT false,
    pass_ttl_minutes  integer NOT NULL DEFAULT 1440 CHECK (pass_ttl_minutes BETWEEN 5 AND 43200),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_captcha_sites_tenant ON captcha_sites (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS captcha_counters (
    site_id      text NOT NULL,
    day          date NOT NULL,
    challenge    bigint NOT NULL DEFAULT 0,
    solve        bigint NOT NULL DEFAULT 0,
    verify_ok    bigint NOT NULL DEFAULT 0,
    verify_fail  bigint NOT NULL DEFAULT 0,
    renewals     bigint NOT NULL DEFAULT 0,
    unique_passes bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, day)
);
CREATE INDEX IF NOT EXISTS idx_captcha_counters_day ON captcha_counters (day);
