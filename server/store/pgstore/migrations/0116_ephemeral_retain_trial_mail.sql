-- 0116: Suresi dolan gecici tunel saklama + deneme teklifi e-postasi isareti
-- 1) tunnels.expired_at: gecici tunelin suresi dolup trafige kapandigi an.
--    Supurucu bu andan itibaren saklama suresi (varsayilan 24 sa) sonra tuneli
--    ve otomatik verilmis adini siler. NULL = suresi dolmamis / kalici.
ALTER TABLE tunnels ADD COLUMN IF NOT EXISTS expired_at timestamptz;
-- 2) Deneme teklifi e-postasi (Better Auth "user" tablosu) bir kez gonderilir.
ALTER TABLE "user" ADD COLUMN IF NOT EXISTS "trialOfferSentAt" timestamptz;
-- Mevcut kullanicilar teklifi eski kurala gore kayitta zaten aldi: tekrar yollanmasin.
UPDATE "user" SET "trialOfferSentAt" = now() WHERE "trialOfferSentAt" IS NULL;
