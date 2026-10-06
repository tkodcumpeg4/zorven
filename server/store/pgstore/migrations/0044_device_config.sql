-- 0044: Uzaktan ajan yapilandirmasi — FAZ 3 / F15
--
-- Panelden cihaz ayarlari. Ajan baglandiginda ve ayar degistiginde
-- config_update ile itilir.
--
-- GUVENLIK ILKESI (kodda da uygulanir): uzaktan yapilandirma yalnizca
-- KISITLAYABILIR. Yerel olarak --no-terminal ile kapatilmis bir izni sunucu
-- ACAMAZ. Aksi halde sunucuyu ele geciren biri, kullanicinin kasitla
-- kapattigi uzak kabugu geri acabilirdi.

CREATE TABLE IF NOT EXISTS device_config (
    client_id  text PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_device_config_tenant ON device_config(tenant_id);
