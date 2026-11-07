package agent

import (
	"net/http"
	"testing"
)

func TestNormalizeOriginProtocol(t *testing.T) {
	cases := map[string]string{
		"": OriginAuto, "auto": OriginAuto, "AUTO": OriginAuto, "bilinmeyen": OriginAuto,
		"http1": OriginHTTP1, "H2": OriginH2, " h2c ": OriginH2C,
	}
	for in, want := range cases {
		if got := NormalizeOriginProtocol(in); got != want {
			t.Errorf("Normalize(%q) = %q, beklenen %q", in, got, want)
		}
	}
	if !ValidOriginProtocol("") || !ValidOriginProtocol("h2c") || ValidOriginProtocol("h3") {
		t.Error("ValidOriginProtocol yanlis")
	}
}

func TestIsGRPCRequestHeader(t *testing.T) {
	mk := func(ct string) http.Header { h := http.Header{}; h.Set("Content-Type", ct); return h }
	if !isGRPCRequest(mk("application/grpc")) || !isGRPCRequest(mk("application/grpc+proto")) {
		t.Error("gRPC tanimlanmali")
	}
	if isGRPCRequest(mk("application/grpc-web+proto")) || isGRPCRequest(mk("application/json")) {
		t.Error("grpc-web / json gRPC degil")
	}
}

func TestOriginClientSelection(t *testing.T) {
	cs := &clientSession{http: &http.Client{}}
	cs.originProto = OriginAuto

	// yetenek yok (eski sunucu): auto modda gRPC bile h2 kullanmaz
	if c, h2 := cs.originClient("http", true); h2 || c != cs.http {
		t.Error("yetenek yokken h2 secilmemeli")
	}
	cs.httpTrailers = true
	if _, h2 := cs.originClient("http", false); h2 {
		t.Error("gRPC olmayan istek auto modda HTTP/1.1 kalmali")
	}
	c, h2 := cs.originClient("http", true)
	if !h2 || c != cs.origin.h2c {
		t.Error("http:// gRPC -> h2c istemcisi beklenir")
	}
	c, h2 = cs.originClient("https", true)
	if !h2 || c != cs.origin.h2tls {
		t.Error("https:// gRPC -> TLS h2 istemcisi beklenir")
	}
	cs.originProto = OriginHTTP1
	if _, h2 := cs.originClient("http", true); h2 {
		t.Error("http1 modunda h2 kullanilmamali")
	}
	cs.originProto = OriginH2C
	if _, h2 := cs.originClient("http", false); !h2 {
		t.Error("h2c modunda her istek h2 olmali")
	}
}
