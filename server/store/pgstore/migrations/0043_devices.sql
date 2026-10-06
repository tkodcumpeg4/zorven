-- 0043: Istemci -> Cihaz modeli — FAZ 3 / F14
--
-- Bu bilgilerin cogu zaten canli baglantidan geliyordu ama baglanti kopunca
-- kayboluyordu. Kalicilastirmak, cihaz OFFLINE ikenken de "bu neydi, en son ne
-- zaman gorundu, hangi surumdeydi" sorularini cevaplanabilir kilar.
--
-- Hicbiri NOT NULL degil: eski ajanlar bu alanlari gondermez ve gondermemeleri
-- hata degildir.

ALTER TABLE clients ADD COLUMN IF NOT EXISTS hostname      text;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS os            text;   -- "windows"
ALTER TABLE clients ADD COLUMN IF NOT EXISTS arch          text;   -- "amd64"
ALTER TABLE clients ADD COLUMN IF NOT EXISTS ips           text[];
ALTER TABLE clients ADD COLUMN IF NOT EXISTS agent_version text;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS last_metrics  jsonb;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS last_seen_at  timestamptz;

-- "En son gorulme" siralamasi cihaz listesinin varsayilan sirasi olacak.
CREATE INDEX IF NOT EXISTS idx_clients_last_seen ON clients(tenant_id, last_seen_at DESC NULLS LAST);
