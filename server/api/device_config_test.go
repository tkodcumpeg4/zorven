package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestValidateAgentSettings(t *testing.T) {
	cases := []struct {
		name    string
		in      protocol.AgentSettings
		wantErr bool
	}{
		{"bos set gecerli", protocol.AgentSettings{}, false},
		{"gecerli log seviyesi", protocol.AgentSettings{LogLevel: "debug"}, false},
		{"bozuk log seviyesi", protocol.AgentSettings{LogLevel: "verbose"}, true},
		{"metrik alt sinir", protocol.AgentSettings{MetricsIntervalSec: minMetricsIntervalSec}, false},
		{"metrik altinda", protocol.AgentSettings{MetricsIntervalSec: 1}, true},
		{"metrik ustunde", protocol.AgentSettings{MetricsIntervalSec: maxMetricsIntervalSec + 1}, true},
		{"metrik sifir = degistirme", protocol.AgentSettings{MetricsIntervalSec: 0}, false},
		{"backoff gecerli", protocol.AgentSettings{ReconnectMaxBackoffSec: 30}, false},
		{"backoff ustunde", protocol.AgentSettings{ReconnectMaxBackoffSec: maxBackoffSec + 1}, true},
		{"backoff sifir = degistirme", protocol.AgentSettings{ReconnectMaxBackoffSec: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateAgentSettings(c.in)
			if (got != "") != c.wantErr {
				t.Errorf("validateAgentSettings = %q, hata beklentisi %v", got, c.wantErr)
			}
		})
	}
}

func TestValidLogLevel(t *testing.T) {
	for _, ok := range []string{"", "debug", "info", "warn", "error"} {
		if !validLogLevel(ok) {
			t.Errorf("%q kabul edilmeliydi", ok)
		}
	}
	for _, bad := range []string{"DEBUG", "trace", "fatal", "bilinmeyen"} {
		if validLogLevel(bad) {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}
