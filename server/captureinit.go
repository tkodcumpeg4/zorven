package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// Istek govdesi kalici yakalama kurulumu (K2). Ortam degiskenleri:
//
//	ZORVEN_CAPTURE_MAX_BODY          govde tavani bayt (varsayilan 262144 = 256 KB)
//	ZORVEN_CAPTURE_TENANT_QUOTA_MB   kiraci basina kota MB (varsayilan 200; 0 = sinirsiz)
//	ZORVEN_CAPTURE_GLOBAL_LIMIT_GB   toplam tavan GB (varsayilan 20; 0 = sinirsiz)
//	ZORVEN_CAPTURE_CACHE_MB          bellek sicak onbellek butcesi MB (varsayilan 32)
//	ZORVEN_CAPTURE_TABLESPACE        (opsiyonel) tabloyu bu tablespace'e tasir
//	ZORVEN_CAPTURE_RETENTION_DAYS    (opsiyonel) plan saklamasina ust sinir (gun)
const keyPurposeCapture = "zorven-capture-v1"

type captureEnv struct {
	MaxBody          int
	TenantQuotaBytes int64
	GlobalLimitBytes int64
	CacheBytes       int64
	Tablespace       string
	RetentionDays    int
}

func envNum(get func(string) string, key string, def int64) int64 {
	v := strings.TrimSpace(get(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		slog.Warn("gecersiz yakalama ayari, varsayilan kullaniliyor", "env", key, "deger", v)
		return def
	}
	return n
}

func captureEnvFrom(get func(string) string) captureEnv {
	return captureEnv{
		MaxBody:          int(envNum(get, "ZORVEN_CAPTURE_MAX_BODY", reqlog.DefaultCaptureMaxBody)),
		TenantQuotaBytes: envNum(get, "ZORVEN_CAPTURE_TENANT_QUOTA_MB", 200) << 20,
		GlobalLimitBytes: envNum(get, "ZORVEN_CAPTURE_GLOBAL_LIMIT_GB", 20) << 30,
		CacheBytes:       envNum(get, "ZORVEN_CAPTURE_CACHE_MB", reqlog.DefaultCaptureCacheBytes>>20) << 20,
		Tablespace:       strings.TrimSpace(get("ZORVEN_CAPTURE_TABLESPACE")),
		RetentionDays:    int(envNum(get, "ZORVEN_CAPTURE_RETENTION_DAYS", 0)),
	}
}

// captureCodecFromEnv, ZORVEN_SECRET_KEY'den (base64, 32 bayt) turetilmis
// anahtarla kodlayici uretir; anahtar yok/gecersizse nil (kalici yakalama kapali).
func captureCodecFromEnv(get func(string) string) *reqlog.CaptureCodec {
	raw := strings.TrimSpace(get("ZORVEN_SECRET_KEY"))
	if raw == "" {
		return nil
	}
	master, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(master) != 32 {
		return nil
	}
	c, err := reqlog.NewCaptureCodec(deriveKey(master, keyPurposeCapture))
	if err != nil {
		return nil
	}
	return c
}

// setupCaptures, sicak onbellegi yapilandirir ve (Postgres + anahtar varsa)
// kalici deposunu baglar. Yazma dongusunu ctx ile baslatir.
func setupCaptures(ctx context.Context, captures *reqlog.CaptureStore, st any, get func(string) string, log *slog.Logger) {
	cfg := captureEnvFrom(get)
	captures.SetLimits(cfg.MaxBody, cfg.CacheBytes)
	pgstore.SetCaptureRetentionMaxDays(cfg.RetentionDays)
	pg, ok := st.(*pgstore.Store)
	if !ok {
		log.Info("kalici yakalama yok: Postgres deposu degil; yalniz bellek onbellegi")
		return
	}
	run, ok := captures.AttachBackend(pg, captureCodecFromEnv(get), reqlog.CapturePersistConfig{
		TenantQuotaBytes: cfg.TenantQuotaBytes, GlobalLimitBytes: cfg.GlobalLimitBytes,
	}, log)
	if !ok {
		return
	}
	if cfg.Tablespace != "" {
		if err := pg.MoveCaptureTablespace(ctx, cfg.Tablespace); err != nil {
			log.Warn("request_captures tablespace'e tasinamadi; mevcut konumda devam", "tablespace", cfg.Tablespace, "hata", err)
		}
	}
	go run(ctx)
	log.Info("kalici istek govdesi yakalama etkin", "govde_tavan_bayt", cfg.MaxBody,
		"kiraci_kota_bayt", cfg.TenantQuotaBytes, "global_tavan_bayt", cfg.GlobalLimitBytes)
}
