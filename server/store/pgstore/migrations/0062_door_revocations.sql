-- 0060: web door - oturum iptali (revocation) ve plan nedeniyle kapanma.
--
-- tunnel_door_revocations: (tunel, kimlik) basina SON iptal zamani. Kapi host'u,
-- oturumun verilis zamani bu zamandan once/esit ise oturumu gecersiz sayar (grant iptal
-- edilen kisi eski cerezle kapiyi yeniden acamasin).
-- tunnel_doors.plan_closed: plan Pro altina dusunce kapi kalici kapatildi (panel/kapi sayfasi icin).

CREATE TABLE IF NOT EXISTS tunnel_door_revocations (
    tunnel_id  VARCHAR(32) NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    identity   text        NOT NULL DEFAULT '',  -- kucuk harfli kullanici adi / e-posta
    revoked_at timestamptz NOT NULL,
    PRIMARY KEY (tunnel_id, identity)
);

ALTER TABLE tunnel_doors ADD COLUMN IF NOT EXISTS plan_closed boolean NOT NULL DEFAULT false;
