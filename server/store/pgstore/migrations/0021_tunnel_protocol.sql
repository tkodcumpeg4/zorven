-- FAZ 3 (D2): tünellere protokol + maruziyet modu + rezerve port alanları.
--   proto:       taşınan protokol (http | tcp | udp). Varsayılan http (geriye uyumlu).
--   exposure:    ziyaretçi maruziyeti (auto | port | sni). http için anlamsız (auto).
--   public_port: Mod A (rezerve-port) için atanan TCP/UDP portu; global benzersiz.
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS proto       text    NOT NULL DEFAULT 'http';
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS exposure    text    NOT NULL DEFAULT 'auto';
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS public_port integer;

-- Rezerve port global benzersiz (aynı port iki tünele verilemez).
CREATE UNIQUE INDEX IF NOT EXISTS tunnels_public_port_uniq
  ON tunnels (public_port) WHERE public_port IS NOT NULL;
