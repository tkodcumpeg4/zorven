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
)

// UsageEvent, veri duzleminde olusan bir bant genisligi tuketim olayi.
type UsageEvent struct {
	TenantID  string
	Type      UsageType
	BytesIn   int64
	BytesOut  int64
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
