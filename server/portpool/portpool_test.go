package portpool

import (
	"testing"
	"time"
)

func TestLegacyDefault(t *testing.T) {
	c, err := Parse("", "", 0, 0, -1, "zorven.app")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Slots) != 1 || c.Slots[0].Key != "" || c.Host("") != "zorven.app" {
		t.Fatalf("legacy yuva yanlis: %+v", c.Slots)
	}
	if c.Min != 10000 || c.Max != 10099 || c.Capacity() != 100 || c.Quarantine != 10*time.Minute {
		t.Fatalf("varsayilanlar yanlis: %+v", c)
	}
	bind, lp, ok := c.Listen("", 10005)
	if !ok || bind != "" || lp != 10005 {
		t.Fatalf("legacy dinleme: %q %d %v", bind, lp, ok)
	}
}

func TestMultiIPCapacityAndHosts(t *testing.T) {
	c, err := Parse("203.0.113.7,198.51.100.7",
		"tcp.zorven.app=203.0.113.7, tcp2.zorven.app=198.51.100.7", 0, 0, -1, "zorven.app")
	if err != nil {
		t.Fatal(err)
	}
	if c.Capacity() != 200 {
		t.Fatalf("kapasite %d", c.Capacity())
	}
	if c.Slots[0].Key != "" || c.Slots[1].Key != "198.51.100.7" {
		t.Fatalf("anahtarlar: %+v", c.Slots)
	}
	if c.Host("") != "tcp.zorven.app" || c.Host("198.51.100.7") != "tcp2.zorven.app" {
		t.Fatalf("hostlar yanlis")
	}
	if got := c.Format("198.51.100.7", 10003); got != "tcp2.zorven.app:10003" {
		t.Fatalf("format: %s", got)
	}
	bind, lp, _ := c.Listen("198.51.100.7", 10003)
	if bind != "198.51.100.7" || lp != 10003 {
		t.Fatalf("dinleme: %s %d", bind, lp)
	}
}

func TestBridgeOffset(t *testing.T) {
	c, err := Parse("203.0.113.7+0,198.51.100.7+10000", "", 0, 0, -1, "zorven.app")
	if err != nil {
		t.Fatal(err)
	}
	bind, lp, _ := c.Listen("198.51.100.7", 10005)
	if bind != "" || lp != 20005 {
		t.Fatalf("kaydirma: %q %d", bind, lp)
	}
	bind, lp, _ = c.Listen("", 10005)
	if bind != "" || lp != 10005 {
		t.Fatalf("birincil: %q %d", bind, lp)
	}
	if c.Host("") != "zorven.app" || c.Host("198.51.100.7") != "198.51.100.7" {
		t.Fatalf("varsayilan hostlar: %s %s", c.Host(""), c.Host("198.51.100.7"))
	}
	if _, err := Parse("1.1.1.1+0,2.2.2.2+50", "", 0, 0, -1, ""); err == nil {
		t.Fatal("ortusen joker araliklar reddedilmeli")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ bind, hosts string }{
		{"abc", ""},
		{"1.1.1.1,1.1.1.1", ""},
		{"1.1.1.1", "a.b=2.2.2.2"},
		{"1.1.1.1", "nohostequals"},
		{"1.1.1.1+x", ""},
	}
	for _, tc := range cases {
		if _, err := Parse(tc.bind, tc.hosts, 0, 0, -1, "z.app"); err == nil {
			t.Errorf("hata beklenirdi: %+v", tc)
		}
	}
	if _, err := Parse("", "", 500, 600, -1, ""); err == nil {
		t.Error("dusuk port reddedilmeli")
	}
	if _, err := Parse("", "", 11000, 10000, -1, ""); err == nil {
		t.Error("min>max reddedilmeli")
	}
}

func TestHostsOnlyDerivesIPs(t *testing.T) {
	c, err := Parse("", "a.z.app=1.1.1.1,b.z.app=2.2.2.2", 0, 0, -1, "")
	if err != nil || len(c.Slots) != 2 || c.Slots[1].Host != "b.z.app" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestPickFillsPrimaryThenNextAndSkipsQuarantine(t *testing.T) {
	c, _ := Parse("1.1.1.1,2.2.2.2", "", 10000, 10001, -1, "z.app")
	used := map[Addr]bool{}
	quar := map[Addr]bool{{Key: "", Port: 10001}: true}
	a, ok := c.Pick(used, quar, nil)
	if !ok || a != (Addr{"", 10000}) {
		t.Fatalf("ilk: %+v", a)
	}
	used[a] = true
	a, ok = c.Pick(used, quar, nil)
	if !ok || a != (Addr{"2.2.2.2", 10000}) {
		t.Fatalf("ikinci: %+v", a)
	}
	used[a] = true
	used[Addr{"2.2.2.2", 10001}] = true
	if _, ok := c.Pick(used, quar, nil); ok {
		t.Fatal("havuz dolu olmali")
	}
	st := c.Stats(used, quar)
	if st.Capacity != 4 || st.Used != 3 || st.Quarantined != 1 || st.Free != 0 {
		t.Fatalf("stats: %+v", st)
	}
}
