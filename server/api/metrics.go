package api

import (
	"net/http"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.3 — per-tünel metrikler.
//
//	GET /api/v1/tunnels/{id}/metrics?window=1h|6h|24h|7d
//
// Zaman dilimlerine (bucket) bölünmüş istek sayısı, hata (5xx) sayısı, ortalama/
// en yüksek gecikme ve bant genişliği döner. Panel bunları grafikler; ayrıca
// pencere geneli özet + hata oranı hesaplanır.

// windowSpec, bir zaman penceresinin süresi ve dilim genişliği (saniye).
type windowSpec struct {
	dur       time.Duration
	bucketSec int
}

var metricWindows = map[string]windowSpec{
	"1h":  {time.Hour, 60},            // 60 dilim, 1 dk
	"6h":  {6 * time.Hour, 300},       // 72 dilim, 5 dk
	"24h": {24 * time.Hour, 900},      // 96 dilim, 15 dk
	"7d":  {7 * 24 * time.Hour, 3600}, // 168 dilim, 1 saat
}

func (s *Server) getTunnelMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tunnelID := r.PathValue("id")
	// Tünel bu kiracıya mı ait? (IDOR önlemi)
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, tunnelID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "tunel bulunamadi")
		return
	}

	spec, ok := metricWindows[r.URL.Query().Get("window")]
	if !ok {
		spec = metricWindows["1h"]
	}
	since := time.Now().Add(-spec.dur)

	// Ham TCP/UDP tunelleri request_logs'a yazmaz: `raw` bolumu dakikalik
	// ozetlerden gelir, HTTP dilimleri bos kalir.
	if tun.Proto == store.ProtoTCP || tun.Proto == store.ProtoUDP {
		raw, rerr := s.rawMetrics(r, tenantID, tun, since, spec.bucketSec)
		if rerr != nil {
			s.fail(w, rerr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"window":     r.URL.Query().Get("window"),
			"bucket_sec": spec.bucketSec,
			"buckets":    []reqlog.MetricBucket{},
			"summary": map[string]any{
				"total_requests": 0, "error_count": 0, "error_rate_pct": 0.0, "avg_ms": 0.0, "max_ms": 0,
			},
			"raw": raw,
		})
		return
	}

	buckets, err := s.Store.TunnelMetrics(r.Context(), tenantID, tunnelID, since, spec.bucketSec)
	if err != nil {
		s.fail(w, err)
		return
	}
	if buckets == nil {
		buckets = []reqlog.MetricBucket{} // JSON'da null yerine [] dönsün (panel guard)
	}

	// Pencere geneli özet.
	var total, errors, maxMs int64
	var durWeighted float64
	for _, b := range buckets {
		total += b.Count
		errors += b.ErrorCount
		durWeighted += b.AvgMs * float64(b.Count)
		if b.MaxMs > maxMs {
			maxMs = b.MaxMs
		}
	}
	avgMs := 0.0
	if total > 0 {
		avgMs = durWeighted / float64(total)
	}
	errorRate := 0.0
	if total > 0 {
		errorRate = float64(errors) / float64(total) * 100
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"window":     r.URL.Query().Get("window"),
		"bucket_sec": spec.bucketSec,
		"buckets":    buckets,
		"summary": map[string]any{
			"total_requests": total,
			"error_count":    errors,
			"error_rate_pct": errorRate,
			"avg_ms":         avgMs,
			"max_ms":         maxMs,
		},
	})
}
