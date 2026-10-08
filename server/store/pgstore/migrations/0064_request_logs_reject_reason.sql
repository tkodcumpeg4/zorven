-- 0064: Ingress'in proxy'lemeden reddettigi istekler de request_logs'a yazilir;
-- reject_reason nedenini tasir (bos = normal proxy'lenen istek).

ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS reject_reason text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_request_logs_rejected ON request_logs (tenant_id, ts DESC) WHERE reject_reason <> '';
