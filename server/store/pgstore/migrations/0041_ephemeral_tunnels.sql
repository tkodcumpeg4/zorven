-- 0041: Gecici (ephemeral) tuneller — FAZ 2 / F07
--
-- CI/CD, demo ve webhook testi icin kisa omurlu tunel. TTL dolunca arka plan
-- supurucusu satiri siler; hostname ve binding'ler CASCADE ile gider.

ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS ephemeral  boolean NOT NULL DEFAULT false;
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS expires_at timestamptz;

-- Kismi indeks: kalici tuneller (cogunluk) indekse hic girmez, supurucu
-- sorgusu yalnizca gercekten suresi olan satirlari tarar.
CREATE INDEX IF NOT EXISTS idx_tunnels_expires ON tunnels(expires_at)
    WHERE expires_at IS NOT NULL;
