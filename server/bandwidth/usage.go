package bandwidth

import (
	"context"
	"time"
)

// UsageType, kullanilan servis/protokol turu.
type UsageType string

const (
	UsageTypeProxyHTTP    UsageType = "proxy_http"
	UsageTypeScreenStream UsageType = "screen_stream"
	UsageTypeShell        UsageType = "shell"
	UsageTypeTCP          UsageType = "proxy_tcp"
	UsageTypeUDP          UsageType = "proxy_udp"
	UsageTypeNetRelay     UsageType = "net_relay"
)

// UsageEvent, veri duzleminde olusan bir bant genisligi tuketim olayi.
type UsageEvent struct {
	TenantID string
	Type     UsageType
	BytesIn  int64
	BytesOut int64
	// App, olayin uygulama tunelinden (zapp://) geldigini belirtir; trafik
	// toplam sayaca ek olarak uygulama sayacina da yazilir (0106).
	App       bool
	Timestamp time.Time
}

// UsageRecorder, kullanim olaylarini kaydeden soyutlama.
// Tek sunucuda LocalUsageRecorder, ileride coklu sunucu (horizontal scaling)
// devreye girdiginde DistributedUsageRecorder (Usage Aggregator / Redis / Event Stream)
// olarak calisabilir.
type UsageRecorder interface {
	Record(ctx context.Context, event UsageEvent) error
	Close() error
}

// AppTargetPrefix, Zorven Apps tunel hedef onegi (apps.TargetScheme ile ayni;
// import dongusu olmasin diye burada tekrarlanir).
const AppTargetPrefix = "zapp://"

// IsAppTarget, tunel hedefinin uygulama yonetimli (zapp://) olup olmadigi.
func IsAppTarget(target string) bool {
	return len(target) >= len(AppTargetPrefix) && target[:len(AppTargetPrefix)] == AppTargetPrefix
}
