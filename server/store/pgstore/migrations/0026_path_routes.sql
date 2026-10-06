-- FAZ 6.5: yol tabanlı yönlendirme (path-based routing).
--   Bir hostname'e, yol ön-ekine göre FARKLI tünellere yönlendiren kurallar
--   eklenebilir (ör. api.example.com/v1 → tünel A, /admin → tünel B). Eşleşen en
--   uzun ön-ek kazanır; hiçbiri eşleşmezse hostname'in birincil (varsayılan) tüneli.
--
--   Regresyon: yol kuralı olmayan hostname'ler eskisi gibi tek tünele gider.
CREATE TABLE IF NOT EXISTS tunnel_path_routes (
    id          text PRIMARY KEY,
    tenant_id   text NOT NULL,
    fqdn        text NOT NULL,
    path_prefix text NOT NULL,
    tunnel_id   text NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (fqdn, path_prefix)
);
CREATE INDEX IF NOT EXISTS idx_path_routes_fqdn ON tunnel_path_routes (fqdn);
