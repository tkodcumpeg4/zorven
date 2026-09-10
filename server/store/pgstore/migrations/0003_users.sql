-- Kullanicilar ve kiraci uyelikleri (GitHub self-servis kayit icin).

CREATE TABLE users (
    id           text PRIMARY KEY,
    github_id    bigint NOT NULL UNIQUE,
    github_login text NOT NULL,
    created_at   timestamptz NOT NULL
);

-- github_id SABITTIR: kisi GitHub kullanici adini degistirse bile ayni hesap
-- kalir. Bu yuzden benzersizlik login'de degil sayisal id'de. login yalnizca
-- gosterim ve arama icin indekslenir.
CREATE INDEX idx_users_login_lower ON users(lower(github_login));

CREATE TABLE tenant_members (
    tenant_id text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id   text NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    role      text NOT NULL,
    PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX idx_tenant_members_user ON tenant_members(user_id);
