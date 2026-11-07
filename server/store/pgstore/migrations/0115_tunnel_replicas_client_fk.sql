-- tunnel_replicas.client_id icin yabanci anahtar (0023'te yoktu).
--   Istemci (cihaz) silinince replika satiri kaliyordu: silinmis istemci hala
--   tunelin replikasi sayiliyor, ham TCP/UDP aday secimi ve tunel/replika
--   listeleri olmayan bir istemciyi gostermeye devam ediyordu.

-- 1) Var olmayan istemciye ait artik satirlar silinir (yoksa FK eklenemez).
DELETE FROM tunnel_replicas r
 WHERE NOT EXISTS (SELECT 1 FROM clients c WHERE c.id = r.client_id);

-- 2) Istemci silinince replika satirlari da gider.
ALTER TABLE tunnel_replicas DROP CONSTRAINT IF EXISTS tunnel_replicas_client_id_fkey;
ALTER TABLE tunnel_replicas ADD CONSTRAINT tunnel_replicas_client_id_fkey
    FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE;

-- 3) FK sutunu: istemci silme (CASCADE) ve ListTunnelsByClient'in replika alt
--    sorgusu (client_id = $1) tum tabloyu taramasin.
CREATE INDEX IF NOT EXISTS idx_tunnel_replicas_client ON tunnel_replicas (client_id);
