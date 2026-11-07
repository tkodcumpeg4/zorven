-- Idempotency-Key kayitlari (kritik POST'lar) ve GitHub X-GitHub-Delivery tekrar
-- kontrolu ("gh:<uuid>" anahtari). 24 saatten eskiler retention gorevinde silinir.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    tenant_id    TEXT        NOT NULL,
    key          TEXT        NOT NULL,
    request_sha  BYTEA       NOT NULL,
    status       INTEGER     NOT NULL DEFAULT 0,  -- 0 = islem suruyor
    response     BYTEA,
    content_type TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created ON idempotency_keys (created_at);

-- Ayni organizasyonda ayni e-postaya iki bekleyen davet olmasin (yaris / cift tik).
-- Once mevcut kopyalari kapat: her (org, e-posta) icin en yeni bekleyen kalir.
UPDATE "invitation" i SET "status" = 'canceled'
 WHERE i."status" = 'pending'
   AND EXISTS (SELECT 1 FROM "invitation" j
                WHERE j."organizationId" = i."organizationId"
                  AND lower(j."email") = lower(i."email")
                  AND j."status" = 'pending'
                  AND (j."createdAt" > i."createdAt"
                       OR (j."createdAt" = i."createdAt" AND j."id" > i."id")));
CREATE UNIQUE INDEX IF NOT EXISTS invitation_pending_org_email_uniq
    ON "invitation" ("organizationId", lower("email")) WHERE "status" = 'pending';
