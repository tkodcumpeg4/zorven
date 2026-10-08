package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// logStore, QueryRequestLogs'a gelen filtreyi yakalar.
type logStore struct {
	store.Store
	got  reqlog.Filter
	rows []reqlog.Entry
}

func (l *logStore) QueryRequestLogs(_ context.Context, f reqlog.Filter) ([]reqlog.Entry, error) {
	l.got = f
	return l.rows, nil
}

func TestListRequests_RejectedFilterParams(t *testing.T) {
	ls := &logStore{rows: []reqlog.Entry{{ID: "r1", Status: 403, RejectReason: "ip_forbidden"}}}
	s := &Server{Store: ls, Log: reqlog.New(10)}

	w := httptest.NewRecorder()
	s.listRequests(w, httptest.NewRequest("GET", "/api/v1/requests?rejected=true", nil))
	if w.Code != http.StatusOK || !ls.got.Rejected || ls.got.Reason != "" {
		t.Fatalf("rejected=true filtreye yansimali: %d %+v", w.Code, ls.got)
	}
	var out []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != 1 || out[0]["reject_reason"] != "ip_forbidden" {
		t.Fatalf("JSON'da reject_reason bekleniyordu: %s", w.Body.String())
	}

	s.listRequests(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/requests?reason=policy_deny", nil))
	if ls.got.Reason != "policy_deny" {
		t.Fatalf("reason filtreye yansimali: %+v", ls.got)
	}

	s.listRequests(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/requests", nil))
	if ls.got.Rejected || ls.got.Reason != "" {
		t.Fatalf("filtresiz istekte reddedilen filtresi olmamali: %+v", ls.got)
	}
}

func TestListRequests_RingFallbackFilter(t *testing.T) {
	ring := reqlog.New(10)
	ring.Add(reqlog.Entry{TunnelID: "t", Status: 200})
	ring.Add(reqlog.Entry{TunnelID: "t", Status: 403, RejectReason: "policy_deny"})
	ring.Add(reqlog.Entry{TunnelID: "t", Status: 502, RejectReason: "client_offline"})
	s := &Server{Log: ring} // Store nil => bellek ring

	count := func(q string) int {
		w := httptest.NewRecorder()
		s.listRequests(w, httptest.NewRequest("GET", "/api/v1/requests"+q, nil))
		var out []reqlog.Entry
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return len(out)
	}
	if n := count(""); n != 3 {
		t.Errorf("filtresiz 3, %d", n)
	}
	if n := count("?rejected=true"); n != 2 {
		t.Errorf("rejected=true 2, %d", n)
	}
	if n := count("?reason=client_offline"); n != 1 {
		t.Errorf("reason=client_offline 1, %d", n)
	}
}
