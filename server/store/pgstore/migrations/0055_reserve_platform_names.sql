-- 0055_reserve_platform_names.sql
-- Platformun kendi alt alanlari (control/pazarlama/analytics/ziyaretci hostlari)
-- reserved_names listesinde DEGILDI (0004 yalnizca www/api/app/... iceriyordu).
-- Bir kiraci global ad olarak `panel` veya `analytics` alip tuneli TCP+SNI
-- moduna cevirirse :443 SNI demux'u panel TLS trafigini (oturum cerezleri
-- dahil) kiracinin ajanina koprulayebiliyordu. Bu adlar artik kiralanamaz.
--
-- Mevcut catisan hostname satirlari SILINMEZ (veri kaybi riski, kiraci
-- trafigi); yalnizca uyari olarak raporlanir. Calisma zamaninda sunucu bu
-- hostlari zaten kendi TLS'ine/kontrol duzlemine birakir (main.go).
INSERT INTO reserved_names (name) VALUES
    ('panel'), ('analytics'), ('umami'), ('console'), ('portal'),
    ('webmail'), ('pop'), ('pop3'), ('mx'), ('ns'), ('ns3'), ('ns4'),
    ('autoconfig'), ('autodiscover'), ('_dmarc'), ('_domainkey'),
    ('postmaster'), ('hostmaster'), ('webmaster'), ('abuse'), ('security'),
    ('sso'), ('oauth'), ('accounts'), ('signup'), ('register'), ('signin'),
    ('metrics'), ('grafana'), ('cluster'), ('internal'), ('localhost'),
    ('install'), ('download'), ('downloads'), ('get'), ('update'), ('updates'),
    ('releases'), ('registry'), ('zorven'), ('rpshell'), ('visitor'), ('tunnel')
ON CONFLICT (name) DO NOTHING;

-- Catisan mevcut kayitlari raporla (SILME YOK). Global adlar <ad>.<platform>
-- bicimindedir; ilk etiket rezerve listedeyse uyari basilir.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT h.id, h.fqdn, h.tenant_id
        FROM hostnames h
        JOIN reserved_names n ON n.name = lower(split_part(h.fqdn, '.', 1))
        WHERE h.type = 'global'
    LOOP
        RAISE WARNING 'rezerve ad ile catisan hostname (silinmedi): id=% fqdn=% tenant=%',
            r.id, r.fqdn, r.tenant_id;
    END LOOP;
END $$;
