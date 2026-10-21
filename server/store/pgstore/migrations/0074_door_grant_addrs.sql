-- Web door: bir grant'in diger IP ailesindeki (IPv4/IPv6) ek adresi.
-- Ziyaretci tarayicidan IPv4 ile kapiyi acar, oyun/SSH istemcisi IPv6 ile baglanabilir (ya da tersi);
-- kapi sayfasi door4/door6 yan kanalindan diger adresi ogrenir ve AYNI grant'e baglar.
-- Grant iptal/sure dolumu/silme (CASCADE) ek adresleri de kapsar.
CREATE TABLE IF NOT EXISTS tunnel_door_grant_addrs (
    grant_id   text        NOT NULL REFERENCES tunnel_door_grants(id) ON DELETE CASCADE,
    family     smallint    NOT NULL CHECK (family IN (4, 6)),
    ip         text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (grant_id, family)
);
CREATE INDEX IF NOT EXISTS idx_tdga_ip ON tunnel_door_grant_addrs (ip);
