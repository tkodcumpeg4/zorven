-- 0010_cluster.sql: Cok dugumlu (multi-node) VPS kumesi icin dugum ve oturum kayit tablolari

CREATE TABLE IF NOT EXISTS cluster_nodes (
    id VARCHAR(64) PRIMARY KEY,
    role VARCHAR(32) NOT NULL,
    address VARCHAR(255) NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cluster_sessions (
    client_id VARCHAR(64) PRIMARY KEY,
    node_id VARCHAR(64) NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cluster_sessions_node ON cluster_sessions(node_id);
