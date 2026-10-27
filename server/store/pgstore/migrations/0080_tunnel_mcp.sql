-- 0080: tunel "MCP" modu. HTTP tuneline (proto=http kalir) baglanan MCP ayarlari:
-- yol, tasima, kimlik dogrulama (oauth|apikey|none), arac izin/ret listesi,
-- hiz siniri, zaman asimlari. Ingress bu ham JSON'u Reload'da BIR KEZ parse eder.
CREATE TABLE IF NOT EXISTS tunnel_mcp (
    tunnel_id  text PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
    config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled    boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);
