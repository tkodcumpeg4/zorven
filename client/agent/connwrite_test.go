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
