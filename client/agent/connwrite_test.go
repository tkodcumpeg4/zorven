package agent

import (
	"context"
	"testing"
	"time"
)

// Istek baglaminin iptali yazim baglamina tasinmamali (coder/websocket iptalde
// tum sunucu baglantisini kapatir); onceden iptal edilmisse yazim hic baslamaz.
func TestConnWriteCtxDetachesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wctx, wcancel, err := connWriteCtx(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wcancel()
	cancel()
	if wctx.Err() != nil {
		t.Fatal("cagiranin iptali yazim baglamini iptal etmemeli")
	}
	if _, ok := wctx.Deadline(); !ok {
		t.Fatal("yazim baglaminda zaman asimi olmali")
	}
	if _, _, err := connWriteCtx(ctx, time.Second); err == nil {
		t.Fatal("iptal edilmis baglamla yazim reddedilmeli")
	}
}

// WS yolu ikinci kez kodlanmamali: /a%2Fb yerel servise /a%2Fb olarak gider.
func TestWSRequestURLKeepsEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"/a%2Fb":          "/a%2Fb?x=1",
		"/u/%40kullanici": "/u/%40kullanici?x=1",
		"/plain/path":     "/plain/path?x=1",
		"/yuzde%zz":       "/yuzde%25zz?x=1", // cozulemeyen eski bicim: eski davranis
	} {
		if got := wsRequestURL("127.0.0.1:80", in, "x=1").RequestURI(); got != want {
			t.Errorf("%s: %s, beklenen %s", in, got, want)
		}
	}
}
