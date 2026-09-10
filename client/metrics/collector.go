package metrics

import (
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Collector, sistem donanım metriklerini toplayan arayüz.
type Collector interface {
	Collect() *protocol.Metrics
}

var (
	defaultCollector Collector
	collectorOnce    sync.Once
)

// Default, platforma uygun varsayılan metrik toplayıcıyı döner.
func Default() Collector {
	collectorOnce.Do(func() {
		defaultCollector = newOSCollector()
	})
	return defaultCollector
}

// Collect, varsayılan toplayıcı üzerinden metrikleri alır.
func Collect() *protocol.Metrics {
	return Default().Collect()
}
