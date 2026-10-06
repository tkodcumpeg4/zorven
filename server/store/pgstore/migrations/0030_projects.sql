-- 0030_projects.sql
-- FAZ 0 (F00): Projects altyapisi

CREATE TABLE IF NOT EXISTS projects (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    slug       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_projects_tenant ON projects (tenant_id);

-- Her mevcut kiraciya bir default projesi ac.
INSERT INTO projects (id, tenant_id, name, slug)
SELECT 'prj_' || substr(md5(t.id || 'default'), 1, 16), t.id, 'Default', 'default'
FROM tenants t
ON CONFLICT DO NOTHING;

-- Kaynak tablolarina project_id ekle ve mevcut kayitlari default projeye bagla.
ALTER TABLE tunnels   ADD COLUMN IF NOT EXISTS project_id text;
ALTER TABLE hostnames ADD COLUMN IF NOT EXISTS project_id text;
ALTER TABLE clients   ADD COLUMN IF NOT EXISTS project_id text;

UPDATE tunnels   SET project_id = (SELECT id FROM projects p WHERE p.tenant_id = tunnels.tenant_id   AND p.slug='default') WHERE project_id IS NULL;
UPDATE hostnames SET project_id = (SELECT id FROM projects p WHERE p.tenant_id = hostnames.tenant_id AND p.slug='default') WHERE project_id IS NULL;
UPDATE clients   SET project_id = (SELECT id FROM projects p WHERE p.tenant_id = clients.tenant_id   AND p.slug='default') WHERE project_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_tunnels_project   ON tunnels   (project_id);
CREATE INDEX IF NOT EXISTS idx_hostnames_project ON hostnames (project_id);
CREATE INDEX IF NOT EXISTS idx_clients_project   ON clients   (project_id);
