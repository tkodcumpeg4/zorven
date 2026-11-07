package main

import (
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
)

func TestConfigSetServerPins(t *testing.T) {
	c := config.DefaultConfig()
	if c.ServerPinned {
		t.Fatal("varsayilan sabit degil")
	}
	if err := c.Set("server", "x.example:443"); err != nil || !c.ServerPinned {
		t.Fatalf("set server sabitlemeli: %v %+v", err, c)
	}
	if err := c.Unset("server"); err != nil || c.ServerPinned {
		t.Fatalf("unset server sabitligi kaldirmali: %v %+v", err, c)
	}
}

func TestApplyNodeConfig(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZORVEN_SERVER", "")
	cfg := config.Config{ServerAddr: "a:1", Servers: []string{"b:1"}}

	ag := &agent.Agent{}
	applyNodeConfig(ag, cfg, "", false)
	if ag.ServerPinned || len(ag.Servers) != 1 || !strings.HasSuffix(ag.NodeCachePath, "nodes.json") {
		t.Fatalf("liste modu: %+v", ag)
	}

	for name, f := range map[string]func() (config.Config, string){
		"bayrak": func() (config.Config, string) { return cfg, "c:1" },
		"ayar":   func() (config.Config, string) { c := cfg; c.ServerPinned = true; return c, "" },
	} {
		ag := &agent.Agent{}
		c, flag := f()
		applyNodeConfig(ag, c, flag, false)
		if !ag.ServerPinned || len(ag.Servers) != 0 || ag.NodeCachePath != "" {
			t.Errorf("%s: sabit olmali: %+v", name, ag)
		}
	}

	t.Setenv("ZORVEN_SERVER", "env:1")
	ag = &agent.Agent{}
	applyNodeConfig(ag, cfg, "", false)
	if !ag.ServerPinned {
		t.Fatal("ZORVEN_SERVER sabitlemeli")
	}
}

// servers listesi config.json birlestirmesinde korunur ve eski "server_addr" ile bir arada okunur.
func TestServersConfigMergeKeepsList(t *testing.T) {
	old := []byte(`{"server_addr":"a:1","servers":["b:1","c:1"],"token":"t","extra_unknown":1}`)
	res, err := config.MergeJSON(old, config.Config{ServerAddr: "a:1", Servers: []string{"b:1", "c:1"}, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.Data)
	if !strings.Contains(s, `"servers"`) || !strings.Contains(s, "c:1") || !strings.Contains(s, "extra_unknown") {
		t.Fatalf("birlestirme: %s", s)
	}
}
