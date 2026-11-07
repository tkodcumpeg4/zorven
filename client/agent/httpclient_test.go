package agent

import (
	"testing"
	"time"
)

func TestNewHTTPTransportTimeouts(t *testing.T) {
	tr := NewHTTPTransport(nil)
	if tr.TLSHandshakeTimeout != 10*time.Second || tr.ResponseHeaderTimeout != 30*time.Second || tr.DialContext == nil || tr.Proxy == nil {
		t.Fatalf("zaman asimlari/vekil eksik: %+v", tr)
	}
	if NewHTTPClient().Timeout != 0 {
		t.Fatal("genel Timeout olmamali (buyuk indirmeler ctx ile sinirlanir)")
	}
	if defaultUpdateClient.Transport == nil {
		t.Fatal("guncelleme istemcisi varsayilan tasiyiciyi kullaniyor")
	}
}
