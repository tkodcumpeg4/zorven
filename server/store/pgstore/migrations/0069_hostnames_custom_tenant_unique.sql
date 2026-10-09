-- F-11 devami: 0067 dogrulanmamis ozel domainleri global benzersizlikten
-- cikardi; ayni kiracinin ayni ozel domaini iki kez eklemesi yine engellenmeli.
-- 0067 oncesi indeks global oldugu icin mevcut veride kiraci ici kopya olamaz.
CREATE UNIQUE INDEX IF NOT EXISTS idx_hostnames_custom_tenant_fqdn
    ON hostnames (tenant_id, lower(fqdn))
    WHERE type = 'custom';
