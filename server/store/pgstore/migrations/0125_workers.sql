-- 0125 (acik surum): worker katmani ticari katmandir ve bu surumde YOK; worker tablolari olusturulmaz.
-- Ortak istek gunlugu semasi icin yalniz request_logs.worker kolonu eklenir (acik reqlog/pgstore kodu
-- istegi karsilayan worker adini okur/yazar; acikta her zaman bos kalir).
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS worker text NOT NULL DEFAULT '';
