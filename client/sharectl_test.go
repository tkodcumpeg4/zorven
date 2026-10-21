package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/localctl"
)

func stub(t *testing.T, ep *localctl.Endpoint, ferr error, call func(localctl.Request) (localctl.Response, error)) *bytes.Buffer {
	t.Helper()
	of, oc, ow := findAgent, callAgent, outW
	t.Cleanup(func() { findAgent, callAgent, outW = of, oc, ow })
	findAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return ep, nil, ferr }
	callAgent = func(_ localctl.Endpoint, r localctl.Request) (localctl.Response, error) { return call(r) }
	var b bytes.Buffer
	outW = &b
	return &b
}

func TestShareViaAgentRunning(t *testing.T) {
	var got localctl.Request
	out := stub(t, &localctl.Endpoint{Service: true}, nil, func(r localctl.Request) (localctl.Response, error) {
		got = r
		return localctl.Response{OK: true, Shares: []localctl.ShareInfo{{ID: "s1", Name: "db", Port: 5432, Proto: "tcp",
			To: []localctl.Grantee{{Type: "user", ID: "ali@x.com"}}, Persistent: true}}}, nil
	})
	found, err := shareViaAgent(localctl.ShareSpec{Name: "db", Port: 5432})
	if !found || err != nil || got.Op != "share_create" || got.Share.Port != 5432 {
		t.Fatalf("found=%v err=%v req=%+v", found, err, got)
	}
	s := out.String()
	for _, want := range []string{"s1", "user:ali@x.com", "zorven access db:5432 --local 5432", "kalici"} {
		if !strings.Contains(s, want) {
			t.Errorf("cikti %q icermiyor:\n%s", want, s)
		}
	}
}

func TestShareViaAgentNotRunningFallsBack(t *testing.T) {
	called := false
	stub(t, nil, nil, func(localctl.Request) (localctl.Response, error) { called = true; return localctl.Response{}, nil })
	found, err := shareViaAgent(localctl.ShareSpec{Port: 1})
	if found || err != nil || called {
		t.Fatalf("calisan ajan yokken gomulu yola dusmeli: %v %v %v", found, err, called)
	}
	// Soket var ama yanit yok: yine gomulu yol (uyari stderr'e).
	stub(t, nil, errors.New("sock: reddedildi"), nil)
	if found, err := shareViaAgent(localctl.ShareSpec{Port: 1}); found || err != nil {
		t.Fatalf("erisilemeyen ajan icin gomulu yol bekleniyordu: %v %v", found, err)
	}
}

func TestShareErrorMapping(t *testing.T) {
	cases := map[string]string{
		"bilinmeyen islem":        "desteklemiyor",
		localctl.ErrNoNet.Error(): "Zorven Net etkin degil",
		"yetkisiz: uid":           "ayni kullanici",
		"port yasak (policy)":     "port yasak (policy)",
	}
	for in, want := range cases {
		stub(t, &localctl.Endpoint{}, nil, func(localctl.Request) (localctl.Response, error) {
			return localctl.Response{}, errors.New(in)
		})
		found, err := shareViaAgent(localctl.ShareSpec{Port: 1})
		if !found || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q -> found=%v err=%v, %q bekleniyordu", in, found, err, want)
		}
	}
}

func TestShareRmLsPaths(t *testing.T) {
	// Ajan yok: net hata.
	stub(t, nil, nil, nil)
	if err := newShareRmCmd().RunE(nil, []string{"db"}); err == nil || !strings.Contains(err.Error(), "bulunamadi") {
		t.Errorf("rm ajansiz: %v", err)
	}
	if err := newShareLsCmd().RunE(nil, nil); err == nil {
		t.Error("ls ajansiz hata vermeliydi")
	}
	// Ajan var.
	out := stub(t, &localctl.Endpoint{}, nil, func(r localctl.Request) (localctl.Response, error) {
		switch r.Op {
		case "share_delete":
			if r.ShareID != "db" {
				t.Errorf("id/ad iletilmedi: %+v", r)
			}
			return localctl.Response{OK: true, Shares: []localctl.ShareInfo{{ID: "s1", Name: "db"}}}, nil
		case "share_list":
			return localctl.Response{OK: true, Shares: []localctl.ShareInfo{{ID: "s1", Name: "db", Port: 5432, Proto: "tcp", Allow: []string{"100.64.0.2"}}}}, nil
		}
		return localctl.Response{}, errors.New("beklenmeyen")
	})
	if err := newShareRmCmd().RunE(nil, []string{"db"}); err != nil || !strings.Contains(out.String(), "silindi: db (s1)") {
		t.Errorf("rm: %v %q", err, out.String())
	}
	out.Reset()
	if err := newShareLsCmd().RunE(nil, nil); err != nil || !strings.Contains(out.String(), "zorven access db:5432 --local 5432") || !strings.Contains(out.String(), "1 es") {
		t.Errorf("ls: %v %q", err, out.String())
	}
}

func TestAccessViaAgent(t *testing.T) {
	var reqs []localctl.Request
	stub(t, &localctl.Endpoint{}, nil, func(r localctl.Request) (localctl.Response, error) {
		reqs = append(reqs, r)
		return localctl.Response{OK: true, Local: r.Local}, nil
	})
	found, err := accessViaAgent([]string{"db=15432", "web:80=127.0.0.1:8080"})
	if !found || err != nil || len(reqs) != 2 || reqs[0].Local != "127.0.0.1:15432" || reqs[1].Target != "web:80" {
		t.Fatalf("found=%v err=%v reqs=%+v", found, err, reqs)
	}
	if _, err := accessViaAgent([]string{"bozuk"}); err == nil {
		t.Error("gecersiz bicim hata vermeliydi")
	}
	stub(t, nil, nil, nil)
	if found, err := accessViaAgent([]string{"db=1"}); found || err != nil {
		t.Errorf("ajansiz gomulu yol: %v %v", found, err)
	}
}
