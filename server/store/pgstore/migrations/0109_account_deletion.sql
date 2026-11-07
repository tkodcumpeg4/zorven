-- 0109: Hesap silme ve kisisel veri disa aktarma icin indeksler.
--
-- Hesap silinirken denetim kaydindaki aktor (e-posta) takma ada cevrilir ve
-- kullanicinin bildirim teslim satirlari silinir; disa aktarma da ayni
-- sutunlardan okur. Indeks yoksa her ikisi de tum tabloyu tarardi.
--
-- Tablolar ticari/kapali katmanda olmayabilir (acik surumde audit_log ve
-- duyuru tablolari yok): yoksa atlanir, migration hata vermez.

DO $$
BEGIN
  IF to_regclass('audit_log') IS NOT NULL THEN
    CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log (actor);
  END IF;
  IF to_regclass('updates_email_deliveries') IS NOT NULL THEN
    CREATE INDEX IF NOT EXISTS updates_email_deliveries_user_idx ON updates_email_deliveries (user_id);
  END IF;
END
$$;
