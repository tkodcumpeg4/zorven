package agent

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Calisirken tavan yukselirse sunucunun daha once istedigi izin hemen etkin olur.
func TestRuntimeEnableAppliesPendingRemoteRequest(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	st.apply(&protocol.AgentSettings{AllowTerminal: boolp(true)}, quiet())
	if st.terminalAllowed() {
		t.Fatal("tavan kapaliyken etkin olmamali")
	}
	c, _, _ := st.setCaps(boolp(true), nil, quiet())
	if !c.TerminalEff || !st.terminalAllowed() {
		t.Error("tavan acilinca bekleyen uzak istek etkin olmadi")
	}
	if st.screenAllowed() {
		t.Error("ekran degismemeliydi")
	}
}

// Sunucu kisitladiysa tavani acmak izni ACMAZ; kapatmak oturum kapatma isaretler.
func TestRuntimeDisableClosesAndRemoteRestrictionHolds(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	st.apply(&protocol.AgentSettings{AllowScreen: boolp(false)}, quiet())
	c, _, _ := st.setCaps(nil, boolp(true), quiet())
	if c.ScreenEff {
		t.Error("uzak kisitlama tavan acilinca asildi")
	}
	st.setCaps(boolp(true), nil, quiet())
	_, ct, cs := st.setCaps(boolp(false), nil, quiet())
	if !ct || cs || st.terminalAllowed() {
		t.Errorf("kapatma beklenen gibi degil ct=%v cs=%v", ct, cs)
	}
}

// Agent.SetCaps: ajan Run oncesi de guvenli; kapatma tavan/etkin durumu dusurur.
func TestAgentSetCapsAndCaps(t *testing.T) {
	a := &Agent{Log: quiet()}
	if a.SetCaps(boolp(true), nil) != (Caps{}) {
		t.Error("Run oncesi bos donmeliydi")
	}
	a.settings = newSettingsState(localCaps{}, true)
	c := a.SetCaps(boolp(true), boolp(true))
	if !c.TerminalEff || !c.ScreenEff || a.Caps() != c {
		t.Errorf("acma basarisiz: %+v", c)
	}
	c = a.SetCaps(boolp(false), nil)
	if c.TerminalEff || !c.ScreenEff {
		t.Errorf("kapatma basarisiz: %+v", c)
	}
}
