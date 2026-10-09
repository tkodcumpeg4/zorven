-- 0067: Imzali oturum cerezleri (github/sso) icin sunucu tarafi iptal sayaci.
-- subject = tenant_id || ':' || user_id. Cerez uretilirken epoch cerezin icine
-- yazilir; logout sayaci artirir ve eski cerezler 401 olur. Satir yoksa epoch 0.

CREATE TABLE IF NOT EXISTS session_epochs (
    subject    TEXT        PRIMARY KEY,
    epoch      BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
