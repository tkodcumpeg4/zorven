-- Better Auth twoFactor plugin semasi (iki adimli dogrulama / 2FA).
--
-- Kullanici ISTEGE BAGLI olarak acar; acinca "user".twoFactorEnabled true olur ve
-- girişte ikinci adim (TOTP authenticator VEYA e-posta OTP VEYA yedek kod) istenir.
-- Sema Better Auth'un bekledigi ad/bicimle birebir (camelCase, tirnakli).

ALTER TABLE "user" ADD COLUMN IF NOT EXISTS "twoFactorEnabled" BOOLEAN DEFAULT false;

-- NOT: Sutun adlari/turleri Better Auth twoFactor plugin semasiyla BIREBIR olmali
-- (verified/failedVerificationCount/lockedUntil dahil), aksi halde plugin her
-- istekte "schema mismatch" firlatip TUM auth'u 500'e dusurur.
CREATE TABLE IF NOT EXISTS "twoFactor" (
    "id"                      TEXT NOT NULL PRIMARY KEY,
    "secret"                  TEXT NOT NULL,
    "backupCodes"             TEXT NOT NULL,
    "userId"                  TEXT NOT NULL REFERENCES "user"("id") ON DELETE CASCADE,
    "verified"                BOOLEAN NOT NULL DEFAULT true,
    "failedVerificationCount" INTEGER NOT NULL DEFAULT 0,
    "lockedUntil"             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS "idx_twofactor_user" ON "twoFactor"("userId");
