package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
)

// applyNodeConfig, ajanin dugum listesi ayarlarini doldurur (Faz 0C-4).
// Sunucu; --server bayragi, ZORVEN_SERVER ortam degiskeni ya da
// `zorven config set server` ile ACIKCA sabitlendiyse liste kullanilmaz.
// service true ise onbellek sistem ayar dizinine, degilse kullanici ayar
// dizinine yazilir.
func applyNodeConfig(ag *agent.Agent, cfg config.Config, flagServer string, service bool) {
	pinned := strings.TrimSpace(flagServer) != "" ||
		strings.TrimSpace(os.Getenv("ZORVEN_SERVER")) != "" ||
		cfg.ServerPinned
	ag.ServerPinned = pinned
	if pinned {
		return
	}
	ag.Servers = cfg.Servers
	dir := ""
	if service {
		dir = filepath.Dir(config.SystemPath())
	} else if p, err := config.Path(); err == nil {
		dir = filepath.Dir(p)
	}
	if dir != "" {
		ag.NodeCachePath = filepath.Join(dir, "nodes.json")
	}
}
