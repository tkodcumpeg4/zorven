package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// projStore: iki projeli sahte depo (pA: t_a, pB: t_b).
type projStore struct {
	store.Store
	got reqlog.Filter
}

func (p *projStore) ListTunnelsByProject(_ context.Context, _, proj string) ([]store.Tunnel, error) {
	if proj == "pA" {
		return []store.Tunnel{{ID: "t_a"}}, nil
	}
	return []store.Tunnel{{ID: "t_b"}}, nil
}

func (p *projStore) QueryRequestLogs(_ context.Context, f reqlog.Filter) ([]reqlog.Entry, error) {
	p.got = f
	return nil, nil
}

func scoped(r *http.Request, tenant, proj string) *http.Request {
	ctx := withTenant(r.Context(), tenant)
	if proj != "" {
		ctx = withProject(ctx, proj)
	}
	return r.WithContext(ctx)
}

func TestListRequests_ProjectScope(t *testing.T) {
	ps := &projStore{}
	s := &Server{Store: ps, Log: reqlog.New(10)}
	s.listRequests(httptest.NewRecorder(), scoped(httptest.NewRequest("GET", "/api/v1/requests", nil), "ten", "pA"))
	if ps.got.ProjectID != "pA" || ps.got.TenantID != "ten" {
		t.Fatalf("proje filtreye yansimali: %+v", ps.got)
	}
	// Proje yoksa suzme yok (mevcut anlam).
	s.listRequests(httptest.NewRecorder(), scoped(httptest.NewRequest("GET", "/api/v1/requests", nil), "ten", ""))
	if ps.got.ProjectID != "" {
		t.Fatalf("proje yokken suzme olmamali: %+v", ps.got)
	}
}

// ringFallbackStore: QueryRequestLogs hata verir => bellek ring'e dusulur.
type ringFallbackStore struct{ projStore }

func (r *ringFallbackStore) QueryRequestLogs(context.Context, reqlog.Filter) ([]reqlog.Entry, error) {
	return nil, context.DeadlineExceeded
}

func TestListRequests_RingProjectScope(t *testing.T) {
	ring := reqlog.New(10)
	ring.Add(reqlog.Entry{TunnelID: "t_a", TenantID: "ten", Status: 200})
	ring.Add(reqlog.Entry{TunnelID: "t_a", TenantID: "ten", Status: 403, RejectReason: "policy_deny"})
	ring.Add(reqlog.Entry{TunnelID: "t_b", TenantID: "ten", Status: 200})
	ring.Add(reqlog.Entry{TenantID: "ten", Status: 404}) // tunelsiz
	s := &Server{Store: &ringFallbackStore{}, Log: ring}

	count := func(proj, q string) int {
		w := httptest.NewRecorder()
		s.listRequests(w, scoped(httptest.NewRequest("GET", "/api/v1/requests"+q, nil), "ten", proj))
		var out []reqlog.Entry
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return len(out)
	}
	if n := count("pA", ""); n != 2 {
		t.Errorf("pA: 2 bekleniyordu (reddedilen dahil), %d", n)
	}
	if n := count("pB", ""); n != 1 {
		t.Errorf("pB: 1 bekleniyordu, %d", n)
	}
	if n := count("pA", "?rejected=true"); n != 1 {
		t.Errorf("pA rejected: 1 bekleniyordu, %d", n)
	}
	if n := count("", ""); n != 4 {
		t.Errorf("projesiz: hepsi (4) bekleniyordu, %d", n)
	}
}

func TestStream_ProjectScope(t *testing.T) {
	br := events.New()
	s := &Server{Store: &projStore{}, Events: br, Log: reqlog.New(10)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.stream(w, scoped(r, "ten", "pA"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	for br.Count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	br.PublishTenant("ten", events.TypeRequestCompleted, reqlog.Entry{ID: "b1", TunnelID: "t_b", TenantID: "ten"})
	br.PublishTenant("ten", events.TypeRequestCompleted, reqlog.Entry{ID: "x1", TenantID: "ten"})
	br.PublishTenant("ten", events.TypeRequestCompleted, reqlog.Entry{ID: "a1", TunnelID: "t_a", TenantID: "ten", RejectReason: "ip_forbidden"})

	sc := bufio.NewScanner(resp.Body)
	var seen []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			seen = append(seen, line)
			break
		}
	}
	if len(seen) != 1 || !strings.Contains(seen[0], `"id":"a1"`) {
		t.Fatalf("yalniz pA kaydi (a1) gelmeliydi: %v", seen)
	}
}
