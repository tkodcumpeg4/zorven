-- 0046: Zorven Network — ozel kaynaklar (FAZ 3 / F17, Asama 1)
--
-- Ozel kaynak = exposure='private' olan bir TCP tuneli. Hostname'i ve public
-- portu YOKTUR; internetten erisilemez. Yalnizca yetkili kullanici "zorven
-- connect" (yerel SOCKS5) uzerinden, kaynagin adiyla (or. db.internal) ulasir.
--
-- Tasima karari: docs/zorven-network-transport.md (SOCKS5 + WSS).

ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS private_name text;

-- Ad kiraci icinde tekil (buyuk/kucuk duyarsiz): SOCKS istegi adla cozulur,
-- iki kaynak ayni adi tasirsa hangisine gidilecegi belirsiz olurdu.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tunnels_private_name
    ON tunnels(tenant_id, lower(private_name))
    WHERE private_name IS NOT NULL;
