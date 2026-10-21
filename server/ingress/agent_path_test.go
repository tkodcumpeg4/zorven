package ingress

import (
	"net/url"
	"testing"
)

// Ajana giden yol istemcinin kodlamasini korur; "@" her surum ajanin kabul
// edecegi bicimde %40 olarak gider (eski ajanlar ham "@"yi reddediyordu).
func TestAgentPath(t *testing.T) {
	cases := map[string]string{
		"/":                  "/",
		"/u/@ali":            "/u/%40ali",
		"/u/%40ali":          "/u/%40ali",
		"/a%2Fb/c":           "/a%2Fb/c",
		"/bosluk%20var":      "/bosluk%20var",
		"/@scope/paket/-/x":  "/%40scope/paket/-/x",
		"/turkce/%C3%A7alis": "/turkce/%C3%A7alis",
	}
	for in, want := range cases {
		u, err := url.Parse("https://ornek.zorven.app" + in)
		if err != nil {
			t.Fatal(err)
		}
		if got := agentPath(u); got != want {
			t.Errorf("agentPath(%q) = %q, beklenen %q", in, got, want)
		}
	}
}
