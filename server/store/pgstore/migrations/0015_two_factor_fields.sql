-- 0014'un ilk surumu twoFactor tablosunu eksik olusturmustu (Better Auth
-- verified/failedVerificationCount/lockedUntil kolonlarini da bekliyor). Bu
-- migration, o eksik semayla olusmus veritabanlarini tamamlar. Idempotent:
-- 0014'un guncel surumunu calistiran taze kurulumlarda no-op olur.

ALTER TABLE "twoFactor" ADD COLUMN IF NOT EXISTS "verified"                BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE "twoFactor" ADD COLUMN IF NOT EXISTS "failedVerificationCount" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "twoFactor" ADD COLUMN IF NOT EXISTS "lockedUntil"             TIMESTAMPTZ;
