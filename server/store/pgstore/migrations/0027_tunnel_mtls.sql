-- FAZ 6.6: karşılıklı TLS (mTLS) / istemci sertifikası.
--   Bir tünele erişim için ziyaretçinin, verilen CA tarafından imzalanmış bir
--   istemci sertifikası sunması zorunlu kılınabilir (güçlü erişim denetimi).
--   TLS el sıkışmasında SNI'ye göre uygulanır; yalnızca enabled=true tüneller
--   için ClientAuth istenir — diğer hostlar/HTTPS etkilenmez.
--
--   Regresyon: kaydı olmayan / enabled=false tüneller normal TLS ile çalışır.
CREATE TABLE IF NOT EXISTS tunnel_mtls (
    tunnel_id  text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    enabled    boolean NOT NULL DEFAULT false,
    ca_pem     text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
