-- 0016: Auth lansman engelleri (#5) — kok-neden duzeltmeleri.
--
-- (A) invitation.inviterId artik NULL olabilir: davetler bazen kiraci-kapsamli
--     super-admin (admin key) tarafindan yapilir; onun Better Auth "user" satiri
--     olmayabilir. FK ihlali yerine NULL kaydediyoruz.
--
-- (B) Org'suz mevcut kullanicilara KISISEL organizasyon veriyoruz. Onceki
--     tasarimda org'suz bir Better Auth kullanicisi VerifySession'da "ten_default"
--     kiracisina dusuyor, bu da onu yanlislikla Platform Admin yapiyordu. Her
--     kullanicinin kendi kiracisi olmali; kimse default'a dusmemeli.
--     (organization INSERT'i trg_sync_organization_to_tenants ile tenants'a da yansir.)

ALTER TABLE "invitation" ALTER COLUMN "inviterId" DROP NOT NULL;

DO $$
DECLARE
  u RECORD;
  new_org_id TEXT;
  new_slug TEXT;
  base_slug TEXT;
BEGIN
  FOR u IN
    SELECT usr."id" AS uid, usr."email" AS uemail, usr."name" AS uname
    FROM "user" usr
    WHERE NOT EXISTS (SELECT 1 FROM "member" m WHERE m."userId" = usr."id")
  LOOP
    new_org_id := 'org_' || substr(md5(u.uid || '|org|' || clock_timestamp()::text), 1, 16);

    -- Okunabilir ama benzersiz bir slug uret.
    base_slug := regexp_replace(lower(split_part(u.uemail, '@', 1)), '[^a-z0-9-]+', '-', 'g');
    IF base_slug IS NULL OR base_slug = '' THEN
      base_slug := 'team';
    END IF;
    new_slug := base_slug || '-' || substr(md5(u.uid), 1, 6);
    WHILE EXISTS (SELECT 1 FROM "organization" WHERE lower("slug") = lower(new_slug)) LOOP
      new_slug := base_slug || '-' || substr(md5(u.uid || clock_timestamp()::text || random()::text), 1, 10);
    END LOOP;

    INSERT INTO "organization" ("id", "name", "slug", "createdAt")
    VALUES (
      new_org_id,
      COALESCE(NULLIF(u.uname, ''), split_part(u.uemail, '@', 1)),
      new_slug,
      now()
    );

    INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
    VALUES (
      'mem_' || substr(md5(u.uid || '|mem|' || clock_timestamp()::text), 1, 16),
      new_org_id,
      u.uid,
      'owner',
      now()
    );
  END LOOP;
END $$;
