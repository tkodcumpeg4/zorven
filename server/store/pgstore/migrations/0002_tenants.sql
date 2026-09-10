-- Kiracilik: tenants tablosu + mevcut kayitlarin varsayilan kiraciya devri.

CREATE TABLE tenants (
    id         text PRIMARY KEY,
    slug       text NOT NULL,
    created_at timestamptz NOT NULL
);

-- slug buyuk/kucuk harf duyarsiz benzersiz (ileride subdomain'de kullanilacak).
CREATE UNIQUE INDEX idx_tenants_slug_lower ON tenants(lower(slug));

-- Varsayilan kiraci: goc oncesi tum kayitlar buna devredilir.
INSERT INTO tenants (id, slug, created_at)
VALUES ('ten_default', 'default', now())
ON CONFLICT (id) DO NOTHING;

-- DEFAULT ile eklemek mevcut satirlari otomatik doldurur (backfill).
ALTER TABLE clients ADD COLUMN tenant_id text NOT NULL DEFAULT 'ten_default'
    REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE tunnels ADD COLUMN tenant_id text NOT NULL DEFAULT 'ten_default'
    REFERENCES tenants(id) ON DELETE CASCADE;

CREATE INDEX idx_clients_tenant ON clients(tenant_id);
CREATE INDEX idx_tunnels_tenant ON tunnels(tenant_id);

-- NOT: tunnels.hostname benzersizligi GLOBAL kalir (idx_tunnels_hostname_lower),
-- kiraci basina degil. DNS global bir isim uzayidir; ayni hostname iki kiraciya
-- verilemez.
