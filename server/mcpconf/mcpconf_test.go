package mcpconf

import "testing"

func TestGlob(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"echo", "echo", true}, {"echo", "echo2", false}, {"db_*", "db_read", true}, {"db_*", "db_", true},
		{"db_*", "xdb_read", false}, {"*", "", true}, {"*", "herhangi", true}, {"a?c", "abc", true},
		{"a?c", "ac", false}, {"*_read", "db_read", true}, {"*_read", "db_reads", false},
		{"a*b*c", "aXXbYYc", true}, {"a*b*c", "aXXbYY", false}, {"Echo", "echo", false},
		{"**", "x", true}, {"a*", "a", true},
	}
	for _, c := range cases {
		if got := Glob(c.p, c.s); got != c.want {
			t.Errorf("Glob(%q,%q)=%v", c.p, c.s, got)
		}
	}
}

func TestToolAllowed(t *testing.T) {
	c := Default()
	if !c.ToolAllowed("herhangi") {
		t.Fatal("bos politika her seye izin vermeli")
	}
	c.AllowTools = []string{"read_*", "echo", "!read_secret"}
	c.DenyTools = []string{"echo2"}
	for n, want := range map[string]bool{"read_file": true, "echo": true, "read_secret": false, "echo2": false, "write": false} {
		if got := c.ToolAllowed(n); got != want {
			t.Errorf("%s=%v", n, got)
		}
	}
	c = Default()
	c.DenyTools = []string{"drop_*"}
	if c.ToolAllowed("drop_table") || !c.ToolAllowed("select") {
		t.Fatal("yalniz deny listesi")
	}
}

func TestCleanPath(t *testing.T) {
	good := map[string]string{"/mcp": "/mcp", "/mcp/": "/mcp", "/": "/", "/v1/mcp": "/v1/mcp"}
	for in, want := range good {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "mcp", "/a/../b", "/a//b", "/a?x=1", "/a b", "/.well-known/x", "/_zva/x", "/a%2fb", "/ü", "/a/."} {
		if _, err := CleanPath(bad); err == nil {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}

func TestNormalizeAndParse(t *testing.T) {
	c, err := Parse([]byte(`{}`))
	if err != nil || c.Auth != AuthOAuth || c.Path != "/mcp" || c.HeadTimeoutSec != 300 || !c.AllowOrgMembers {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{
		`{"auth":"none"}`, `{"auth":"x"}`, `{"head_timeout_sec":9999}`, `{"rate_limit_per_min":-1}`,
		`{"transport":"ws"}`, `{"max_request_bytes":99999999999}`, `{"allow_tools":["a b"]}`, `{not json`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s kabul edildi", bad)
		}
	}
	if c, err := Parse([]byte(`{"auth":"none","ack_no_auth":true}`)); err != nil || c.Auth != AuthNone {
		t.Fatalf("%v", err)
	}
	// Bozuk ayar ingress'te en kisitlayici davranisa duser ve izin listesini korur.
	l := ParseLenient([]byte(`{"auth":"none","allow_tools":["a"],"head_timeout_sec":99999}`))
	if l.Auth != AuthOAuth || len(l.AllowTools) != 1 {
		t.Fatalf("%+v", l)
	}
}

func TestCanonicalResource(t *testing.T) {
	cases := map[[2]string]string{
		{"Acme.Zorven.App", "/mcp"}:      "acme.zorven.app/mcp",
		{"acme.zorven.app:443", "/mcp/"}: "acme.zorven.app/mcp",
		{"acme.zorven.app.", "/mcp"}:     "acme.zorven.app/mcp",
		{"h:8443", "/mcp"}:               "h:8443/mcp",
	}
	for in, want := range cases {
		if got := CanonicalResource(in[0], in[1]); got != want {
			t.Errorf("%v -> %q", in, got)
		}
	}
	if k, ok := ResourceKey("https://Acme.Zorven.App/mcp"); !ok || k != "acme.zorven.app/mcp" {
		t.Fatalf("%q %v", k, ok)
	}
	for _, bad := range []string{"ftp://x/y", "https://", "https://u@h/mcp", "//h/mcp", "https://h/mcp?x=1"} {
		if _, ok := ResourceKey(bad); ok {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}
