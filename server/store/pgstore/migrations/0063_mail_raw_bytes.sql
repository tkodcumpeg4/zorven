-- 0063: Webmail ham mesaj baytlari. raw (TEXT) NUL baytlarini ve gecersiz UTF-8'i
-- koruyamaz; raw_bytes (bytea) mesaji birebir saklar. Yeni kayitlar raw_bytes'a
-- yazilir (raw bos kalir); eski satirlar raw'dan okunmaya devam eder.

ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS raw_bytes bytea;
