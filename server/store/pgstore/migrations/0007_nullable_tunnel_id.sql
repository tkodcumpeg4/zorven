-- 0007_nullable_tunnel_id.sql
-- Domainlerin tunelden bagimsiz olusturulabilmesi icin tunnel_id NULL yapilir.
-- Tunel silindiginde domain silinmesin, bosa ciksin (ON DELETE SET NULL).

ALTER TABLE hostnames ALTER COLUMN tunnel_id DROP NOT NULL;

ALTER TABLE hostnames DROP CONSTRAINT IF EXISTS hostnames_tunnel_id_fkey;

ALTER TABLE hostnames ADD CONSTRAINT hostnames_tunnel_id_fkey
    FOREIGN KEY (tunnel_id) REFERENCES tunnels(id) ON DELETE SET NULL;
