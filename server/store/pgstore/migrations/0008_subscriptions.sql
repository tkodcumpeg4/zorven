-- 0008_subscriptions.sql
-- Abonelikler ve Plan Limitleri:
-- Her kiracinin bir aboneligi ve buna bagli kaynak limitleri vardir.

CREATE TABLE IF NOT EXISTS subscriptions (
    tenant_id              text PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    plan                   text NOT NULL DEFAULT 'free',
    status                 text NOT NULL DEFAULT 'active',
    stripe_customer_id     text,
    stripe_subscription_id text,
    current_period_end     timestamptz,
    max_clients            int NOT NULL DEFAULT 1,
    max_custom_domains     int NOT NULL DEFAULT 0,
    max_tunnels            int NOT NULL DEFAULT 1,
    bandwidth_limit_bytes  bigint NOT NULL DEFAULT 5368709120, -- 5 GB
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_plan ON subscriptions(plan);
CREATE INDEX IF NOT EXISTS idx_subscriptions_status ON subscriptions(status);

-- Mevcut tum kiracilara varsayilan Free aboneligi ata.
INSERT INTO subscriptions (
    tenant_id, plan, status, max_clients, max_custom_domains, max_tunnels, bandwidth_limit_bytes, created_at, updated_at
)
SELECT id, 'free', 'active', 1, 0, 1, 5368709120, now(), now()
FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;
