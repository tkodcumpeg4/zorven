-- FAZ 6: trafik politikası (traffic policy).
--   Tünel başına istek/yanıt başlığı ekle-değiştir-sil ve yönlendirme (redirect)
--   kuralları. Ingress hot-path'te uygulanır; kurallar router snapshot'ına
--   parse edilerek gömülür (per-istek JSON parse yok).
--
--   Regresyon: politikası olmayan / enabled=false tüneller aynen çalışır.
CREATE TABLE IF NOT EXISTS tunnel_traffic_policies (
    tunnel_id  text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled    boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);
