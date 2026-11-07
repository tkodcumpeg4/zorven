-- 0122_client_offline_notified.sql: kopuk ajan bildirimi (kiraci owner'larina) bir kez gider.
-- Ajan yeniden baglaninca (el sikisma) NULL'a doner; sunucu yeniden baslasa da tekrar gitmez.
ALTER TABLE clients ADD COLUMN IF NOT EXISTS offline_notified_at TIMESTAMPTZ;
