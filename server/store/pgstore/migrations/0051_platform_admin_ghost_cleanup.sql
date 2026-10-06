-- 0051_platform_admin_ghost_cleanup.sql
-- Platform admin "Yonetime Gec" (adminSwitchTenant) ile bir kiraciya her
-- girdiginde o kiraciya 'owner' rolunde gercek bir member satiri ekleniyordu.
-- Bu satirlar admini diger kiracilarin ekiplerinde sahip olarak gosteriyor ve
-- uye kotasini tuketiyordu. Artik eklenmiyor; mevcut olanlari temizle.
--
-- Yalnizca su satirlar silinir: kullanici ten_default'ta owner/admin (platform
-- admin), org ten_default degil, rol 'owner' ve o org'da BASKA bir owner da var
-- (yani admin'in kendi tek-sahibi oldugu bir org DEGIL).
DELETE FROM "member" m
WHERE m."organizationId" <> 'ten_default'
  AND m."role" = 'owner'
  AND EXISTS (
    SELECT 1 FROM "member" pm
    WHERE pm."organizationId" = 'ten_default'
      AND pm."userId" = m."userId"
      AND pm."role" IN ('owner', 'admin'))
  AND EXISTS (
    SELECT 1 FROM "member" o
    WHERE o."organizationId" = m."organizationId"
      AND o."role" = 'owner'
      AND o."userId" <> m."userId");
