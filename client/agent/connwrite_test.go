package agent

import (
	"testing"
)

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
