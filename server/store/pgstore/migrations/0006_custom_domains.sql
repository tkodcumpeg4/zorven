-- Custom domain destegi: musterinin kendi alan adini baglamasi ve dogrulamasi.
-- hostnames tablosuna verified ve verify_token kolonlari eklenir.

ALTER TABLE hostnames ADD COLUMN IF NOT EXISTS verified boolean NOT NULL DEFAULT true;
ALTER TABLE hostnames ADD COLUMN IF NOT EXISTS verify_token text;

-- On-demand ACME (Let's Encrypt / autocert) sertifika onbellegi (multi-node uyumlu):
CREATE TABLE IF NOT EXISTS acme_cache (
    key        text PRIMARY KEY,
    data       bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_hostnames_verified ON hostnames(verified);
