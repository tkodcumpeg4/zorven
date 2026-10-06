package ingress

import (
	"reflect"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestCandidateClients(t *testing.T) {
	got := candidateClients(store.HostRoute{
		ClientID:         "a",
		ReplicaClientIDs: []string{"b", "a", "", "c"}, // "a" tekrar + boş ayıklanmalı
	})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidateClients = %v; istenen %v", got, want)
	}
}

func TestOrderOnline_RoundRobin(t *testing.T) {
	cands := []string{"a", "b", "c"}
	all := func(string) bool { return true }

	// start=0 → a,b,c ; start=1 → b,c,a ; start=2 → c,a,b ; start=3 → a,b,c
	checks := []struct {
		start uint64
		want  []string
	}{
		{0, []string{"a", "b", "c"}},
		{1, []string{"b", "c", "a"}},
		{2, []string{"c", "a", "b"}},
		{3, []string{"a", "b", "c"}},
	}
	for _, c := range checks {
		got := orderOnline(cands, c.start, all)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("orderOnline(start=%d) = %v; istenen %v", c.start, got, c.want)
		}
	}
}

func TestOrderOnline_Failover(t *testing.T) {
	cands := []string{"a", "b", "c"}
	// Yalnızca "b" ve "c" çevrimiçi; "a" düşmüş → atlanmalı.
	online := map[string]bool{"b": true, "c": true}
	isOnline := func(s string) bool { return online[s] }

	// start=0 sırası a,b,c → a düşük, sonuç b,c
	got := orderOnline(cands, 0, isOnline)
	if !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("failover sırası = %v; istenen [b c]", got)
	}

	// Hiçbiri çevrimiçi değilse nil.
	if got := orderOnline(cands, 0, func(string) bool { return false }); got != nil {
		t.Fatalf("hiç çevrimiçi yokken = %v; istenen nil", got)
	}
}

func TestRoundRobinNext(t *testing.T) {
	var rr roundRobin
	// İlk çağrı 0, sonra artan.
	for i := uint64(0); i < 5; i++ {
		if got := rr.next("host"); got != i {
			t.Fatalf("next(host) çağrı %d = %d; istenen %d", i, got, i)
		}
	}
	// Farklı anahtar bağımsız sayaç.
	if got := rr.next("other"); got != 0 {
		t.Fatalf("next(other) = %d; istenen 0", got)
	}
}
