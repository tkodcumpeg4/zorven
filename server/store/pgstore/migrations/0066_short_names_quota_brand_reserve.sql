-- 0066: Marka rezervasyonu (acik surum). Kisa ad kotasi ve plan dususunde askiya alma
-- ticari katmandadir ve bu surumde YOKTUR: kisa adlar sinirsizdir, askiya alinmaz.
--
-- reserved_names: marka/urun/ozellik sozcukleri. "zorven" / "rpshell" ICEREN her
-- etiket ayrica kod tarafinda (server/store/brandnames.go) desen olarak reddedilir;
-- kendi dagitiminiz icin BrandKeywords listesini ve bu tabloyu duzenleyin.

INSERT INTO reserved_names (name) VALUES
    ('zorven'), ('zorvenapp'), ('zrv'), ('rpshell'), ('door'), ('kapi'),
    ('mail'), ('mail-app'), ('imap'), ('smtp'), ('submission'),
    ('help'), ('destek'), ('support'), ('docs'), ('doc'), ('blog'),
    ('status'), ('statuspage'), ('api-docs'), ('dev'), ('developer'),
    ('developers'), ('gelistirici'), ('bireysel'), ('sirketler'), ('business'),
    ('personal'), ('pricing'), ('fiyatlandirma'), ('enterprise'), ('team'),
    ('pro'), ('hobby'), ('free'), ('billing'), ('invoice'), ('pay'),
    ('payment'), ('checkout'), ('login'), ('logout'), ('auth'), ('verify'),
    ('account'), ('admin'), ('root'), ('system'), ('official'), ('resmi'),
    ('guvenlik'), ('cdn'), ('static'), ('assets'), ('media'), ('img'),
    ('edge'), ('network'), ('connect'), ('forward'), ('gateway'), ('relay'),
    ('vpn'), ('agent'), ('client'), ('desktop'), ('cli'), ('sdk'), ('app'),
    ('apps'), ('www'), ('beta'), ('staging'), ('test'), ('demo'), ('sandbox')
ON CONFLICT (name) DO NOTHING;

-- Marka deseniyle catisan mevcut kayitlari raporla (SILME YOK).
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT id, fqdn, tenant_id FROM hostnames
        WHERE type = 'global'
          AND (replace(lower(split_part(fqdn, '.', 1)), '-', '') LIKE '%zorven%'
            OR replace(lower(split_part(fqdn, '.', 1)), '-', '') LIKE '%rpshell%')
    LOOP
        RAISE WARNING 'marka deseniyle catisan kisa ad (silinmedi): id=% fqdn=% tenant=%',
            r.id, r.fqdn, r.tenant_id;
    END LOOP;
END $$;
