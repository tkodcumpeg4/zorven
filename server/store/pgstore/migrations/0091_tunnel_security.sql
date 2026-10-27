-- 0091: Tunel seviyesinde WAF ve Geo ayari (ZTNA Z3).
-- config: {"waf":{"mode":"off|log|block","body":false,"disabled_rules":[]},
--          "geo":{"mode":"off|allow|deny","countries":["TR"],"unknown_is":"allow|deny"}}
-- Satir yok = her sey kapali. Dogrulama API katmaninda (tunsec.Normalize).
CREATE TABLE IF NOT EXISTS tunnel_security (
    tunnel_id  text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tunnel_security_tenant ON tunnel_security (tenant_id);
