-- 0011_member_tokens.sql
-- Ekip uyelerine ozel istemci erisim jetonlari (Per-Member Client Tokens)

-- 1. clients tablosuna user_id iliskilendirmesi:
ALTER TABLE clients ADD COLUMN IF NOT EXISTS user_id text REFERENCES "user"("id") ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_clients_user ON clients(user_id);
