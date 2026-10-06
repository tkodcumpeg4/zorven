-- FAZ 5: yük dengeleme / yüksek erişilebilirlik (HA).
--   Bir tünel (dolayısıyla hostname'i) birden çok istemci agent tarafından
--   servis edilebilir. tunnel_replicas, tünelin BİRİNCİL client_id'sine EK olarak
--   aynı yükü paylaşan diğer istemcileri tutar. Ingress, çevrimiçi üyeler arasında
--   round-robin dağıtır ve düşen üyeyi atlar (failover).
--
--   Regresyon: replica eklenmemiş tüneller yalnızca birincil client ile çalışır
--   (tam olarak eskisi gibi).
CREATE TABLE IF NOT EXISTS tunnel_replicas (
    tunnel_id  text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    client_id  text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tunnel_id, client_id)
);
CREATE INDEX IF NOT EXISTS idx_tunnel_replicas_tunnel ON tunnel_replicas (tunnel_id);
