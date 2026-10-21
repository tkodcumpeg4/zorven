package api

// Ham TCP tunel metrikleri.
//
//	GET /api/v1/tunnels/{id}/tcp — canli istatistik + son 60 dk dakikalik seri
//
// Ayrica GET /api/v1/tunnels/{id}/metrics ham tunellerde `raw` bolumu doner
// (rawMetrics, bkz. metrics.go).

import (
	"net/http"
	"sort"
	"time"

	"github.com/tkodcumpeg4/zorven/server/rawproxy"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const tcpSeriesWindow = time.Hour

func (s *Server) getTunnelTCP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if tun.Proto != store.ProtoTCP {
		writeJSONError(w, http.StatusConflict, "not_tcp", "bu istatistikler yalnızca TCP tünelleri içindir")
		return
	}
	series, err := s.Store.ListTCPStats(r.Context(), tenantID, tun.ID, time.Now().Add(-tcpSeriesWindow))
	if err != nil {
		s.fail(w, err)
		return
	}
	var live *rawproxy.TCPLiveStats
	if s.TCPStats != nil {
		if ls, ok := s.TCPStats(tun.ID); ok {
			live = &ls
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"live":   live,
		"series": series,
	})
}

// rawBucket, ham (tcp/udp) tunellerin metrik diliminin ortak seklidir; tcp'de
// udp alanlari, udp'de tcp alanlari 0 kalir.
type rawBucket struct {
	Bucket time.Time `json:"bucket"`
	// TCP
	Conns     int64   `json:"conns_new"`
	ConnsPeak int64   `json:"conns_peak"`
	Rejected  int64   `json:"conns_rejected"`
	DurAvgMs  float64 `json:"dur_avg_ms"`
	DurMaxMs  int64   `json:"dur_max_ms"`
	// UDP
	PacketsIn  int64 `json:"packets_in"`
	PacketsOut int64 `json:"packets_out"`
	FlowsNew   int64 `json:"flows_new"`
	FlowsPeak  int64 `json:"flows_peak"`
	Dropped    int64 `json:"dropped"`
	// ortak
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`

	durSum, durCnt int64
}

// rawMetrics, ham tunelin dakikalik ozetlerini bucketSec genisligindeki dilimlere
// toplar ve pencere ozetini (`summary`) hesaplar.
func (s *Server) rawMetrics(r *http.Request, tenantID string, tun store.Tunnel, since time.Time, bucketSec int) (map[string]any, error) {
	byKey := map[int64]*rawBucket{}
	get := func(t time.Time) *rawBucket {
		k := t.Unix() / int64(bucketSec) * int64(bucketSec)
		b := byKey[k]
		if b == nil {
			b = &rawBucket{Bucket: time.Unix(k, 0).UTC()}
			byKey[k] = b
		}
		return b
	}
	sum := rawBucket{}
	switch tun.Proto {
	case store.ProtoTCP:
		rows, err := s.Store.ListTCPStats(r.Context(), tenantID, tun.ID, since)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			b := get(m.Minute)
			b.Conns += m.ConnsNew
			b.Rejected += m.ConnsRejected
			if int64(m.ConnsPeak) > b.ConnsPeak {
				b.ConnsPeak = int64(m.ConnsPeak)
			}
			b.BytesIn += m.BytesIn
			b.BytesOut += m.BytesOut
			b.durSum += m.DurSumMs
			b.durCnt += m.DurCount
			if m.DurMaxMs > b.DurMaxMs {
				b.DurMaxMs = m.DurMaxMs
			}
		}
	case store.ProtoUDP:
		rows, err := s.Store.ListUDPStats(r.Context(), tenantID, tun.ID, since)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			b := get(m.Minute)
			b.PacketsIn += m.PacketsIn
			b.PacketsOut += m.PacketsOut
			b.BytesIn += m.BytesIn
			b.BytesOut += m.BytesOut
			b.FlowsNew += m.FlowsNew
			if int64(m.FlowsPeak) > b.FlowsPeak {
				b.FlowsPeak = int64(m.FlowsPeak)
			}
			b.Dropped += m.DroppedRate + m.DroppedSize + m.DroppedFlows + m.DroppedDatagrams
		}
	}
	out := make([]rawBucket, 0, len(byKey))
	for _, b := range byKey {
		if b.durCnt > 0 {
			b.DurAvgMs = float64(b.durSum) / float64(b.durCnt)
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket.Before(out[j].Bucket) })
	for _, b := range out {
		sum.Conns += b.Conns
		sum.Rejected += b.Rejected
		sum.PacketsIn += b.PacketsIn
		sum.PacketsOut += b.PacketsOut
		sum.FlowsNew += b.FlowsNew
		sum.Dropped += b.Dropped
		sum.BytesIn += b.BytesIn
		sum.BytesOut += b.BytesOut
		sum.durSum += b.durSum
		sum.durCnt += b.durCnt
		if b.ConnsPeak > sum.ConnsPeak {
			sum.ConnsPeak = b.ConnsPeak
		}
		if b.FlowsPeak > sum.FlowsPeak {
			sum.FlowsPeak = b.FlowsPeak
		}
		if b.DurMaxMs > sum.DurMaxMs {
			sum.DurMaxMs = b.DurMaxMs
		}
	}
	if sum.durCnt > 0 {
		sum.DurAvgMs = float64(sum.durSum) / float64(sum.durCnt)
	}
	return map[string]any{
		"proto":   tun.Proto,
		"buckets": out,
		"summary": map[string]any{
			"conns_new":      sum.Conns,
			"conns_peak":     sum.ConnsPeak,
			"conns_rejected": sum.Rejected,
			"dur_avg_ms":     sum.DurAvgMs,
			"dur_max_ms":     sum.DurMaxMs,
			"packets_in":     sum.PacketsIn,
			"packets_out":    sum.PacketsOut,
			"flows_new":      sum.FlowsNew,
			"flows_peak":     sum.FlowsPeak,
			"dropped":        sum.Dropped,
			"bytes_in":       sum.BytesIn,
			"bytes_out":      sum.BytesOut,
		},
	}, nil
}
