-- Metrik uyarisi e-postasinin dili (tr / en / ja / zh).
--   Uyariyi kuran kullanicinin dili kurulum aninda yazilir (kayitli dil tercihi >
--   panel dili cerezi > Accept-Language). Bos = tr (bu migration'dan onceki uyarilar
--   eskisi gibi Turkce gider). Uyari yeniden kaydedilirken istekte dil sinyali yoksa
--   mevcut deger korunur.
ALTER TABLE tunnel_alerts ADD COLUMN IF NOT EXISTS notify_lang text NOT NULL DEFAULT '';
