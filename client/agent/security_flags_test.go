package agent

import (
	"testing"
)

func TestSecurity_Agent_Flags_Defaults(t *testing.T) {
	agDefault := &Agent{}
	if agDefault.NoTerminal {
		t.Error("expected NoTerminal to be false by default")
	}
	if agDefault.NoScreen {
		t.Error("expected NoScreen to be false by default")
	}

	agHardened := &Agent{
		NoTerminal: true,
		NoScreen:   true,
	}
	if !agHardened.NoTerminal || !agHardened.NoScreen {
		t.Error("expected flags to be true when configured")
	}
}
