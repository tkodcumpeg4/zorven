-- FAZ 6.4: metrik uyarıları (alert).
--   Tünel başına hata-oranı eşiği; arka plandaki değerlendirici periyodik olarak
--   son window_min dakikadaki 5xx oranını hesaplar ve eşik aşılırsa (ve en az
--   min_requests istek varsa) bir kez e-posta gönderir. Durum 'firing' iken tekrar
--   bildirmez; oran düşünce 'ok'a döner.
--
--   Regresyon: enabled=false / kaydı olmayan tüneller etkilenmez.
CREATE TABLE IF NOT EXISTS tunnel_alerts (
    tunnel_id        text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    enabled          boolean NOT NULL DEFAULT false,
    error_rate_pct   int NOT NULL DEFAULT 10,
    window_min       int NOT NULL DEFAULT 5,
    min_requests     int NOT NULL DEFAULT 20,
    notify_email     text NOT NULL DEFAULT '',
    state            text NOT NULL DEFAULT 'ok',
    last_changed_at  timestamptz,
    last_notified_at timestamptz
);
