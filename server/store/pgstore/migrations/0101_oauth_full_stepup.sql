-- 0101: OAuth "full" (tam erisim) kapsami icin step-up zamani.
--
-- Kod ve grant, tam erisim onayinin dayandigi taze giris (oturum acilis) zamanini
-- tasir; denetim ve "Bagli uygulamalar" gosterimi icin. NULL = full verilmedi.

ALTER TABLE oauth_codes  ADD COLUMN IF NOT EXISTS step_up_at timestamptz;
ALTER TABLE oauth_grants ADD COLUMN IF NOT EXISTS step_up_at timestamptz;
