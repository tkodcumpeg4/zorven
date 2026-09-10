-- Hostname isim-uzayi: hostname tunelden ayrilip kendi tablosuna tasinir.
-- Bir tunel BIRDEN COK adla yayinlanabilir (kisa global ad + kiraci kapsamli ad).

CREATE TABLE hostnames (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    tunnel_id  text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    fqdn       text NOT NULL,
    -- 'global' : <ad>.<platform>              (kullanicinin sectigi kisa ad)
    -- 'scoped' : <tunel>--<kiraci>.<platform> (her zaman musait)
    -- 'legacy' : goc oncesi serbest hostname  (or. api.localhost)
    -- Plan 5'te 'custom' eklenecek (musterinin kendi domaini).
    type       text NOT NULL,
    created_at timestamptz NOT NULL
);

-- DNS buyuk/kucuk harf duyarsizdir ve isim uzayi GLOBALDIR: ayni fqdn iki
-- kiraciya verilemez. idx_tunnels_hostname_lower'in devami.
CREATE UNIQUE INDEX idx_hostnames_fqdn_lower ON hostnames(lower(fqdn));
CREATE INDEX idx_hostnames_tunnel ON hostnames(tunnel_id);
CREATE INDEX idx_hostnames_tenant ON hostnames(tenant_id);

-- Mevcut tunellerin hostname'lerini tasi. Bunlar platform isim-uzayina
-- uymayabilir (or. api.localhost), o yuzden 'legacy': yonlendirme aynen
-- surer ama yeni ad kurallari bunlara uygulanmaz.
INSERT INTO hostnames (id, tenant_id, tunnel_id, fqdn, type, created_at)
SELECT 'hst_' || substr(md5(random()::text || t.id), 1, 16),
       t.tenant_id, t.id, t.hostname, 'legacy', t.created_at
FROM tunnels t;

-- Tek gercek kaynak artik hostnames. Iki yerde tutmak, birinin sessizce
-- eskimesi demek olurdu.
DROP INDEX IF EXISTS idx_tunnels_hostname_lower;
ALTER TABLE tunnels DROP COLUMN hostname;

CREATE TABLE reserved_names (
    name text PRIMARY KEY
);

-- Platform icin ayrilmis adlar: kimse bunlari kiralayamaz.
INSERT INTO reserved_names (name) VALUES
    ('www'), ('api'), ('admin'), ('app'), ('dashboard'), ('docs'),
    ('mail'), ('smtp'), ('imap'), ('ns1'), ('ns2'), ('static'), ('cdn'),
    ('status'), ('help'), ('support'), ('blog'), ('billing'), ('account'),
    ('login'), ('auth'), ('cname'), ('_acme-challenge')
ON CONFLICT (name) DO NOTHING;
