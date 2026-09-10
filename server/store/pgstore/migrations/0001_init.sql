-- Baslangic semasi: sqlitestore semasinin Postgres karsiligi.
-- Bu asamada TENANCY YOK; davranis SQLite ile birebir ayni.

CREATE TABLE clients (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    token_id   text NOT NULL UNIQUE,
    token_hash text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE tunnels (
    id         text PRIMARY KEY,
    hostname   text NOT NULL,
    client_id  text NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    target     text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL
);

CREATE INDEX idx_tunnels_client ON tunnels(client_id);

-- DNS buyuk/kucuk harf duyarsizdir: API.Example.com ile api.example.com ayni
-- tunele isaret etmeli. SQLite'taki "COLLATE NOCASE UNIQUE" karsiligi.
CREATE UNIQUE INDEX idx_tunnels_hostname_lower ON tunnels(lower(hostname));

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);
