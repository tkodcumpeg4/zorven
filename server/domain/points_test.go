package domain

import (
	"context"
	"errors"
	"net"
	"testing"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func TestPointsToPlatform(t *testing.T) {
	r := fakeResolver{
		"cname.zorven.app": {"203.0.113.7"},
		"api.example.net":      {"203.0.113.7"},
		"yeni.example.net":     {"1.2.3.4"},
		"cift.example.net":     {"1.2.3.4", "203.0.113.7"},
	}
	ctx := context.Background()
	if !PointsToPlatform(ctx, r, "api.example.net", "zorven.app") {
		t.Error("platforma yonlenen ad true olmali")
	}
	if !PointsToPlatform(ctx, r, "CIFT.example.net.", "zorven.app") {
		t.Error("kesisen IP (buyuk harf + nokta) true olmali")
	}
	if PointsToPlatform(ctx, r, "yeni.example.net", "zorven.app") {
		t.Error("baska IP'ye giden ad false olmali")
	}
	if PointsToPlatform(ctx, r, "kayitsiz.example.net", "zorven.app") {
		t.Error("cozulemeyen ad false olmali")
	}
	if PointsToPlatform(ctx, fakeResolver{}, "api.example.net", "zorven.app") {
		t.Error("platform hedefi cozulemezse false olmali")
	}
}
