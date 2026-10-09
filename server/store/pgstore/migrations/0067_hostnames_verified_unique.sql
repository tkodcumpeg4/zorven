-- F-11: dogrulanmamis ozel (custom) hostname'ler kiraciler arasi benzersiz
-- olmamali; aksi halde baskasinin domainini dogrulamadan ekleyip adi
-- rezerve etmek mumkundu. Benzersizlik yalniz platform adlari (global/scoped/
-- legacy) ve DOGRULANMIS ozel domainler icin uygulanir.
DROP INDEX IF EXISTS idx_hostnames_fqdn_lower;
CREATE UNIQUE INDEX IF NOT EXISTS idx_hostnames_fqdn_lower
    ON hostnames (lower(fqdn))
    WHERE type <> 'custom' OR verified;
