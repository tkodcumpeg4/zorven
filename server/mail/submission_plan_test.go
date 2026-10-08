package mail

import (
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
)

// Acik surum: plan siniri yok; herhangi bir kiraci harici aliciya gonderebilir.
func TestSubmission_ExternalAllowedInOpenEdition(t *testing.T) {
	e := newSubEnv(t, "free")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMail(testBox, []string{"kisi@example.org"},
		strings.NewReader(msgFor(testBox, "kisi@example.org", ""))); err != nil {
		t.Fatalf("harici gonderim: %v", err)
	}
	if e.relay.n != 1 || len(e.relay.to) != 1 || e.relay.to[0] != "kisi@example.org" {
		t.Fatalf("relay: %+v", e.relay)
	}
}
