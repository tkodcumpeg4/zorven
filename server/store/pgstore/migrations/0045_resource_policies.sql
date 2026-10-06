-- 0045: Cihaz etiketleri.
-- Kaynak politikalari (etiket+rol erisim kontrolu) ticari katmandadir; acik
-- surumde yalnizca etiket meta verisi tablosu olusturulur.

CREATE TABLE IF NOT EXISTS device_tags (
    client_id  text NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key        text NOT NULL,
    value      text NOT NULL,
    PRIMARY KEY (client_id, key)
);
CREATE INDEX IF NOT EXISTS idx_device_tags_tenant ON device_tags(tenant_id);
