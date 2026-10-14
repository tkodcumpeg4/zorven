package visitorauth

import (
	"testing"
	"time"
)

func TestPolicyFromConfigBounds(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{`{}`, 0}, {``, 0}, {`{"session_ttl_minutes":15}`, 15}, {`{"session_ttl_minutes":43200}`, 43200},
		{`{"session_ttl_minutes":14}`, 0}, {`{"session_ttl_minutes":43201}`, 0}, {`{"session_ttl_minutes":-5}`, 0},
	}
	for _, c := range cases {
		if got := PolicyFromConfig([]byte(c.raw)).TTLMinutes; got != c.want {
			t.Errorf("%q: %d, istenen %d", c.raw, got, c.want)
		}
	}
	if (SessionPolicy{}).TTL() != 12*time.Hour {
		t.Error("varsayilan 12 saat olmali")
	}
	if !ValidSessionMinutes(0) || ValidSessionMinutes(1) || ValidSessionMinutes(43201) || !ValidSessionMinutes(60) {
		t.Error("ValidSessionMinutes sinirlari")
	}
}

func TestAbsoluteCap(t *testing.T) {
	if (SessionPolicy{TTLMinutes: 60}).AbsoluteCap() != 7*24*time.Hour {
		t.Error("kisa TTL icin tavan 7 gun")
	}
	if (SessionPolicy{TTLMinutes: 43200}).AbsoluteCap() != 30*24*time.Hour {
		t.Error("TTL tavandan buyukse TTL tavandir")
	}
}

func TestEvaluateRenewalAndCap(t *testing.T) {
	now := time.Now()
	p := SessionPolicy{TTLMinutes: 60, Sliding: true}
	iat := now.Add(-40 * time.Minute)
	st, ok := p.Evaluate(now, iat.Add(time.Hour).Unix(), iat.UnixMilli(), iat.UnixMilli())
	if !ok || st.Renewal == nil || !st.Renewal.Exp.After(now.Add(59*time.Minute)) {
		t.Fatalf("yenileme bekleniyordu: %+v ok=%v", st, ok)
	}
	if st.Renewal.Orig.UnixMilli() != iat.UnixMilli() {
		t.Error("Orig korunmali")
	}
	p.Sliding = false
	if st, _ := p.Evaluate(now, iat.Add(time.Hour).Unix(), iat.UnixMilli(), iat.UnixMilli()); st.Renewal != nil {
		t.Error("sliding kapaliyken yenileme olmamali")
	}
	p.Sliding = true
	orig := now.Add(-8 * 24 * time.Hour)
	if _, ok := p.Evaluate(now, now.Add(time.Hour).Unix(), now.UnixMilli(), orig.UnixMilli()); ok {
		t.Error("tavan asilinca gecersiz")
	}
	if _, ok := (SessionPolicy{TTLMinutes: 43200}).Evaluate(now, now.Add(time.Hour).Unix(), 0, 0); !ok {
		t.Error("legacy gecerli")
	}
	if _, ok := (SessionPolicy{TTLMinutes: 43200}).Evaluate(now, now.Add(-time.Second).Unix(), 0, 0); ok {
		t.Error("legacy suresi dolmus")
	}
}
