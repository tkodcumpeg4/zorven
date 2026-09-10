-- Better Auth tablolari ve tenants senkronizasyonu.
-- Better Auth standart Postgres semasi: user, session, account, verification, organization, member, invitation.

CREATE TABLE IF NOT EXISTS "user" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "name" TEXT NOT NULL,
    "email" TEXT NOT NULL UNIQUE,
    "emailVerified" BOOLEAN NOT NULL DEFAULT false,
    "image" TEXT,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "updatedAt" TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS "session" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "expiresAt" TIMESTAMPTZ NOT NULL,
    "token" TEXT NOT NULL UNIQUE,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "updatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "ipAddress" TEXT,
    "userAgent" TEXT,
    "userId" TEXT NOT NULL REFERENCES "user"("id") ON DELETE CASCADE,
    "activeOrganizationId" TEXT
);

CREATE INDEX IF NOT EXISTS "idx_session_token" ON "session"("token");
CREATE INDEX IF NOT EXISTS "idx_session_user" ON "session"("userId");

CREATE TABLE IF NOT EXISTS "account" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "accountId" TEXT NOT NULL,
    "providerId" TEXT NOT NULL,
    "userId" TEXT NOT NULL REFERENCES "user"("id") ON DELETE CASCADE,
    "accessToken" TEXT,
    "refreshToken" TEXT,
    "idToken" TEXT,
    "accessTokenExpiresAt" TIMESTAMPTZ,
    "refreshTokenExpiresAt" TIMESTAMPTZ,
    "scope" TEXT,
    "password" TEXT,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "updatedAt" TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS "idx_account_user" ON "account"("userId");

CREATE TABLE IF NOT EXISTS "verification" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "identifier" TEXT NOT NULL,
    "value" TEXT NOT NULL,
    "expiresAt" TIMESTAMPTZ NOT NULL,
    "createdAt" TIMESTAMPTZ DEFAULT now(),
    "updatedAt" TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS "idx_verification_identifier" ON "verification"("identifier");

-- Organizasyon (Multi-tenant) destegi:
CREATE TABLE IF NOT EXISTS "organization" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "name" TEXT NOT NULL,
    "slug" TEXT NOT NULL UNIQUE,
    "logo" TEXT,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "metadata" TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS "idx_organization_slug_lower" ON "organization"(lower("slug"));

CREATE TABLE IF NOT EXISTS "member" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "organizationId" TEXT NOT NULL REFERENCES "organization"("id") ON DELETE CASCADE,
    "userId" TEXT NOT NULL REFERENCES "user"("id") ON DELETE CASCADE,
    "role" TEXT NOT NULL,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS "idx_member_org" ON "member"("organizationId");
CREATE INDEX IF NOT EXISTS "idx_member_user" ON "member"("userId");

CREATE TABLE IF NOT EXISTS "invitation" (
    "id" TEXT NOT NULL PRIMARY KEY,
    "organizationId" TEXT NOT NULL REFERENCES "organization"("id") ON DELETE CASCADE,
    "email" TEXT NOT NULL,
    "role" TEXT,
    "status" TEXT NOT NULL,
    "expiresAt" TIMESTAMPTZ NOT NULL,
    "inviterId" TEXT NOT NULL REFERENCES "user"("id") ON DELETE CASCADE,
    "createdAt" TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS "idx_invitation_org" ON "invitation"("organizationId");

-- Mevcut tenants tablosundaki verileri organization tablosuna aktar:
INSERT INTO "organization" ("id", "name", "slug", "createdAt")
SELECT t.id, t.slug, t.slug, t.created_at
FROM tenants t
ON CONFLICT ("id") DO NOTHING;

-- organization <-> tenants iki yonlu senkronizasyon tetikleyicileri (Triggers):
CREATE OR REPLACE FUNCTION sync_organization_to_tenants()
RETURNS TRIGGER AS $$
BEGIN
    IF pg_trigger_depth() > 1 THEN
        RETURN NEW;
    END IF;
    INSERT INTO tenants (id, slug, created_at)
    VALUES (NEW."id", NEW."slug", NEW."createdAt")
    ON CONFLICT (id) DO UPDATE SET slug = EXCLUDED.slug;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_organization_to_tenants ON "organization";
CREATE TRIGGER trg_sync_organization_to_tenants
AFTER INSERT OR UPDATE ON "organization"
FOR EACH ROW
EXECUTE FUNCTION sync_organization_to_tenants();

CREATE OR REPLACE FUNCTION sync_tenants_to_organization()
RETURNS TRIGGER AS $$
BEGIN
    IF pg_trigger_depth() > 1 THEN
        RETURN NEW;
    END IF;
    INSERT INTO "organization" ("id", "name", "slug", "createdAt")
    VALUES (NEW.id, NEW.slug, NEW.slug, NEW.created_at)
    ON CONFLICT ("id") DO UPDATE SET "slug" = EXCLUDED."slug";
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_tenants_to_organization ON tenants;
CREATE TRIGGER trg_sync_tenants_to_organization
AFTER INSERT OR UPDATE ON tenants
FOR EACH ROW
EXECUTE FUNCTION sync_tenants_to_organization();
