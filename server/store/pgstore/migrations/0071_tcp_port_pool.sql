-- K3: TCP/UDP rezerve-port adres modeli.
--  1) tunnels.public_ip: tahsisin (IP, port) cifti. '' = birincil IP (mevcut tum
--     kayitlar; geriye uyum), aksi halde havuz yuvasinin IP metni.
--  2) Benzersizlik artik (public_ip, public_port) uzerinde: ayni port numarasi
--     farkli IP'lerde kullanilabilir.
--  3) tcp_port_releases: serbest birakilan portun karantinasi. Tetikleyici,
--     tunel silinince / portu birakilinca / adresi degisince satiri gunceller;
--     uygulama "released_at > now() - karantina" olanlari tahsisten dislar.
--  4) plans.max_reserved_ports: kiraci basina rezerve port limiti (NULL = sinirsiz).

ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS public_ip text NOT NULL DEFAULT '';

DROP INDEX IF EXISTS tunnels_public_port_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS tunnels_public_addr_uniq
  ON tunnels (public_ip, public_port) WHERE public_port IS NOT NULL;

CREATE TABLE IF NOT EXISTS tcp_port_releases (
  public_ip   text        NOT NULL,
  public_port integer     NOT NULL,
  released_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (public_ip, public_port)
);

CREATE OR REPLACE FUNCTION tunnels_port_release() RETURNS trigger AS $$
BEGIN
  IF OLD.public_port IS NOT NULL AND (
       TG_OP = 'DELETE'
       OR NEW.public_port IS DISTINCT FROM OLD.public_port
       OR NEW.public_ip IS DISTINCT FROM OLD.public_ip) THEN
    INSERT INTO tcp_port_releases (public_ip, public_port, released_at)
    VALUES (OLD.public_ip, OLD.public_port, now())
    ON CONFLICT (public_ip, public_port) DO UPDATE SET released_at = EXCLUDED.released_at;
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS tunnels_port_release_trg ON tunnels;
CREATE TRIGGER tunnels_port_release_trg
  AFTER UPDATE OF public_port, public_ip OR DELETE ON tunnels
  FOR EACH ROW EXECUTE FUNCTION tunnels_port_release();

ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_reserved_ports integer;
