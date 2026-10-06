-- FAZ 4: kötüye kullanım kontrolü.
--   tunnels.frozen: platform admin bir tüneli dondugroup içeriğini askıya alabilir.
--     Dondurulmuş tünel ingress'te "askıya alındı" sayfası döner (proxy'lenmez).
--   abuse_reports: ziyaretçilerin bir hostname'i kötüye kullanım olarak bildirmesi.
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS frozen boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS abuse_reports (
    id          text PRIMARY KEY,
    fqdn        text NOT NULL,
    reason      text NOT NULL DEFAULT '',
    reporter_ip text NOT NULL DEFAULT '',
    handled     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_abuse_reports_created ON abuse_reports (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_abuse_reports_fqdn ON abuse_reports (fqdn);
