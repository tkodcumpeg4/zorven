-- 0049: Saglik kontrolu + gelismis yuk dengeleme — FAZ 4 / F21
--
-- Tunel basina yuk dengeleme stratejisi ve aktif saglik kontrolu. Kayit
-- yoksa eski davranis: round-robin, saglik kontrolu yok (geriye uyumlu).
-- Saglik DURUMU bellekte tutulur; burada yalnizca yapilandirma var.

CREATE TABLE IF NOT EXISTS tunnel_lb (
    tunnel_id           text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id           text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    strategy            text NOT NULL DEFAULT 'round_robin', -- round_robin|weighted|least_connections|latency
    weights             jsonb NOT NULL DEFAULT '{}'::jsonb,  -- client_id -> agirlik (weighted)
    health_enabled      boolean NOT NULL DEFAULT false,
    health_path         text NOT NULL DEFAULT '/',
    interval_sec        int NOT NULL DEFAULT 10,
    timeout_sec         int NOT NULL DEFAULT 3,
    unhealthy_threshold int NOT NULL DEFAULT 3,
    healthy_threshold   int NOT NULL DEFAULT 2,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tunnel_lb_tenant ON tunnel_lb(tenant_id);
