package smtp

import (
	"strings"
	"testing"
)

// ZORVEN-YAMA: alan adsiz postmaster yamasi testi.
func TestParsePathBarePostmaster(t *testing.T) {
	for _, s := range []string{"<postmaster>", "<PostMaster>", "postmaster"} {
		p := parser{s: s, allowBarePostmaster: true}
		if got, err := p.parsePath(); err != nil || !strings.EqualFold(got, "postmaster") {
			t.Errorf("%q: got %q, %v", s, got, err)
		}
		// yama kapaliyken (MAIL FROM) reddedilmeli
		q := parser{s: s}
		if _, err := q.parsePath(); err == nil {
			t.Errorf("%q: yama kapaliyken kabul edildi", s)
		}
	}
	for _, s := range []string{"<postmasterx>", "<baskasi>", "<postmaster", "<postmaster@>", "<>"} {
		p := parser{s: s, allowBarePostmaster: true}
		if got, err := p.parsePath(); err == nil {
			t.Errorf("%q: kabul edildi: %q", s, got)
		}
	}
	p := parser{s: "<postmaster@example.com>", allowBarePostmaster: true}
	if got, err := p.parsePath(); err != nil || got != "postmaster@example.com" {
		t.Errorf("alanli postmaster: %q, %v", got, err)
	}
}
