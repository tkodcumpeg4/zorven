-- 0009_billing_phase2.sql
-- Veri-Gudumlu Planlar, Gelismis Limitler ve Bant Genisligi Takibi

-- 1. Plans tablosu: Plan kotalari kod icinde hard-code edilmez, veritabaninda yonetilir.
CREATE TABLE IF NOT EXISTS plans (
    id                       text PRIMARY KEY, -- 'free', 'hobby', 'pro', 'team', 'enterprise'
    name                     text NOT NULL,
    price_monthly            int NOT NULL DEFAULT 0, -- USD cent (0, 500, 1200, 2900)
    max_clients              int,                    -- NULL = sinirsiz
    max_tunnels              int,                    -- NULL = sinirsiz
    max_custom_domains       int,                    -- NULL = sinirsiz
    bandwidth_limit_bytes    bigint,                 -- NULL = sinirsiz
    bandwidth_normal_mbps    int NOT NULL DEFAULT 10,
    bandwidth_throttled_mbps int NOT NULL DEFAULT 1,
    max_screen_streams       int,                    -- NULL = sinirsiz
    screen_max_fps           int NOT NULL DEFAULT 30,
    log_retention_days       int NOT NULL DEFAULT 1,
    max_members              int,                    -- NULL = sinirsiz
    has_api_access           boolean NOT NULL DEFAULT false,
    has_ip_allowlist         boolean NOT NULL DEFAULT false,
    extra_device_price       int NOT NULL DEFAULT 0, -- USD cent (+50 cent = $0.50)
    extra_gb_price           int NOT NULL DEFAULT 0, -- USD cent (+10 cent = $0.10)
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

-- Plan tohum verileri (Free, Hobby, Pro, Team, Enterprise)
INSERT INTO plans (
    id, name, price_monthly,
    max_clients, max_tunnels, max_custom_domains, bandwidth_limit_bytes,
    bandwidth_normal_mbps, bandwidth_throttled_mbps,
    max_screen_streams, screen_max_fps, log_retention_days, max_members,
    has_api_access, has_ip_allowlist, extra_device_price, extra_gb_price
) VALUES
    ('free', 'Free', 0,
     2, 2, 0, 5368709120, -- 5 GB
     10, 1,
     1, 30, 1, 1,
     false, false, 0, 0),

    ('hobby', 'Hobby', 500, -- $5/ay
     5, 5, 1, 21474836480, -- 20 GB
     50, 5,
     1, 30, 3, 1,
     false, false, 0, 0),

    ('pro', 'Pro', 1200, -- $12/ay
     15, 20, 5, 107374182400, -- 100 GB
     100, 10,
     2, 30, 7, 3,
     true, true, 0, 0),

    ('team', 'Team', 2900, -- $29/ay
     50, 50, 50, 268435456000, -- 250 GB
     500, 25,
     5, 60, 30, 5,
     true, true, 50, 10),

    ('enterprise', 'Enterprise', 0, -- Ozel teklif
     NULL, NULL, NULL, NULL,
     1000, 100,
     NULL, 60, 90, NULL,
     true, true, 0, 0)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    price_monthly = EXCLUDED.price_monthly,
    max_clients = EXCLUDED.max_clients,
    max_tunnels = EXCLUDED.max_tunnels,
    max_custom_domains = EXCLUDED.max_custom_domains,
    bandwidth_limit_bytes = EXCLUDED.bandwidth_limit_bytes,
    bandwidth_normal_mbps = EXCLUDED.bandwidth_normal_mbps,
    bandwidth_throttled_mbps = EXCLUDED.bandwidth_throttled_mbps,
    max_screen_streams = EXCLUDED.max_screen_streams,
    screen_max_fps = EXCLUDED.screen_max_fps,
    log_retention_days = EXCLUDED.log_retention_days,
    max_members = EXCLUDED.max_members,
    has_api_access = EXCLUDED.has_api_access,
    has_ip_allowlist = EXCLUDED.has_ip_allowlist,
    extra_device_price = EXCLUDED.extra_device_price,
    extra_gb_price = EXCLUDED.extra_gb_price,
    updated_at = now();

-- 2. Subscriptions tablosuna yabanci anahtar ve ozel override kolonlari
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'fk_subscriptions_plan' AND table_name = 'subscriptions'
    ) THEN
        ALTER TABLE subscriptions
            ADD CONSTRAINT fk_subscriptions_plan
            FOREIGN KEY (plan) REFERENCES plans(id) ON UPDATE CASCADE;
    END IF;
END $$;

-- Subscriptions tablosuna ozel kiraci kotalari (NULL ise planin standart kotasi gecerli olur)
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS override_max_clients int,
    ADD COLUMN IF NOT EXISTS override_max_tunnels int,
    ADD COLUMN IF NOT EXISTS override_max_custom_domains int,
    ADD COLUMN IF NOT EXISTS override_bandwidth_limit_bytes bigint;

-- Mevcut Free aboneliklerin limitlerini yeni 2 client / 2 tunnel seviyesine yukselt
UPDATE subscriptions
SET max_clients = 2, max_tunnels = 2, updated_at = now()
WHERE plan = 'free';

-- 3. Tenant Bandwidth tablosu: Donem bazli ('YYYY-MM') kiraci trafik kayitlari
CREATE TABLE IF NOT EXISTS tenant_bandwidth (
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period     text NOT NULL, -- 'YYYY-MM'
    bytes_in   bigint NOT NULL DEFAULT 0,
    bytes_out  bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, period)
);

CREATE INDEX IF NOT EXISTS idx_tenant_bandwidth_period ON tenant_bandwidth(period);
