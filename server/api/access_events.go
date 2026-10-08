package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Tunel ziyaretci erisim istatistikleri (Basic/OAuth giris olaylari).
//
//	GET /api/v1/tunnels/{id}/access-events          (limit, cursor)
//	GET /api/v1/tunnels/{id}/access-events/summary  (window=24h|7d|30d)

// accessEventCursor, (created_at, id) cifti icin opak imleç: base64url("<unixnano>.<id>").
func encodeAccessCursor(ts time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts.UnixNano(), 10) + "." + id))
}

func decodeAccessCursor(c string) (time.Time, string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", false
	}
	ns, id, ok := strings.Cut(string(raw), ".")
	if !ok || id == "" {
		return time.Time{}, "", false
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.Unix(0, n).UTC(), id, true
}

func (s *Server) listAccessEvents(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tunnelID := r.PathValue("id")
	if _, err := s.Store.GetTunnel(r.Context(), tenantID, tunnelID); err != nil {
		s.fail(w, err)
		return
	}
	limit, cursor, paginated := parsePage(r)
	if !paginated {
		limit = defaultPageLimit
	}
	var beforeTS time.Time
	var beforeID string
	if cursor != "" {
		var ok bool
		beforeTS, beforeID, ok = decodeAccessCursor(cursor)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "gecersiz cursor")
			return
		}
	}
	events, err := s.Store.ListAccessEvents(r.Context(), tenantID, tunnelID, limit+1, beforeTS, beforeID)
	if err != nil {
		s.fail(w, err)
		return
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	next := ""
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		next = encodeAccessCursor(last.CreatedAt, last.ID)
	}
	setPageHeaders(w, r, next, hasMore)
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) accessEventsSummary(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tunnelID := r.PathValue("id")
	if _, err := s.Store.GetTunnel(r.Context(), tenantID, tunnelID); err != nil {
		s.fail(w, err)
		return
	}
	window := r.URL.Query().Get("window")
	var d time.Duration
	switch window {
	case "", "24h":
		window, d = "24h", 24*time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window 24h, 7d veya 30d olmali")
		return
	}
	sum, err := s.Store.AccessEventSummary(r.Context(), tenantID, tunnelID, time.Now().UTC().Add(-d))
	if err != nil {
		s.fail(w, err)
		return
	}
	sum.Window = window
	writeJSON(w, http.StatusOK, sum)
}
