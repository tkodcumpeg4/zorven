-- FAZ 1a — Tünel erişim denetimi (auth gating).
-- Her tünel için opsiyonel bir erişim politikası. Kayıt yoksa veya enabled=false
-- ise tünel herkese açıktır (mevcut v1 davranışı — geriye uyumlu).
--
-- mode:
--   none            → denetim yok (varsayılan)
--   basic           → HTTP Basic Auth (config: {"username","password_hash"})
--   oauth           → Google/GitHub ile giriş; config.allowed_emails boşsa
--                     herhangi bir doğrulanmış kullanıcı, doluysa yalnız o e-postalar
--                     (config: {"providers":["github","google"],"allowed_emails":[...]})
-- Not: "email_allowlist" senaryosu mode=oauth + allowed_emails ile karşılanır.
CREATE TABLE IF NOT EXISTS tunnel_access_policies (
    tunnel_id  VARCHAR(32) PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    mode       VARCHAR(30)  NOT NULL DEFAULT 'none',
    config     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    enabled    BOOLEAN      NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
