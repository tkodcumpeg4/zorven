package main

import (
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Her ayar anahtari icin "canli uygulanir mi" sorusuna bilincli bir cevap yazilmis olmali;
// yeni anahtar eklenince bu test ayri bir karar ister.
func TestConfigLiveCoversAllKeys(t *testing.T) {
	for _, k := range config.Keys {
		if _, ok := configLive[k]; !ok {
			t.Errorf("%s: configLive tablosunda canli/yeniden baslatma karari yok", k)
		}
	}
	for k := range configLive {
		if !knownKey(k) {
			t.Errorf("configLive bilinmeyen anahtar iceriyor: %s", k)
		}
	}
	// Ajanin Reload'u (client/agent/obs.go) yalniz bunlari canli uygular.
	for _, k := range []string{"server", "token", "enable_terminal", "enable_screen"} {
		if !liveApplicable(k) {
			t.Errorf("%s canli uygulanabilir olmali", k)
		}
	}
	for _, k := range []string{"insecure", "ca_cert", "enable_deploy", "language"} {
		if liveApplicable(k) {
			t.Errorf("%s canli uygulanamaz (ajan baslarken bir kez okur)", k)
		}
	}
}

func setKeyFn(key, value string) func(*config.Config) error {
	return func(c *config.Config) error { return c.Set(key, value) }
}

func TestConfigSetLiveNotice(t *testing.T) {
	live := func() string { return i18n.T("v3.cfg.live.ok") }
	later := func() string { return i18n.T("v3.cfg.live.later") }
	restart := func() string { return i18n.T("v3.cfg.live.restart") }

	t.Run("enable_deploy canli degil", func(t *testing.T) {
		isolateHome(t)
		reloads := 0
		out := stub(t, &localctl.Endpoint{Service: false}, nil, func(r localctl.Request) (localctl.Response, error) {
			if r.Op == "reload" {
				reloads++
			}
			return localctl.Response{OK: true}, nil
		})
		if err := mutateConfig(false, "enable_deploy", setKeyFn("enable_deploy", "true"), true); err != nil {
			t.Fatal(err)
		}
		s := out.String()
		if reloads != 0 {
			t.Errorf("enable_deploy icin ajana reload gitmemeli: %d", reloads)
		}
		if !strings.Contains(s, later()) || strings.Contains(s, live()) {
			t.Errorf("'yeniden baslatinca uygulanir' bekleniyordu, 'canli uygulandi' degil:\n%s", s)
		}
		// unset de ayni
		out.Reset()
		unset := func(c *config.Config) error { return c.Unset("enable_deploy") }
		if err := mutateConfig(false, "enable_deploy", unset, true); err != nil {
			t.Fatal(err)
		}
		if s := out.String(); !strings.Contains(s, later()) || strings.Contains(s, live()) {
			t.Errorf("unset icin de yeniden baslatma notu bekleniyordu:\n%s", s)
		}
	})

	t.Run("insecure ve ca_cert canli degil", func(t *testing.T) {
		isolateHome(t)
		out := stub(t, &localctl.Endpoint{Service: false}, nil, func(localctl.Request) (localctl.Response, error) {
			t.Error("canli uygulanamayan anahtar icin ajana istek gitmemeli")
			return localctl.Response{}, nil
		})
		if err := mutateConfig(false, "insecure", setKeyFn("insecure", "true"), true); err != nil {
			t.Fatal(err)
		}
		if s := out.String(); !strings.Contains(s, later()) || strings.Contains(s, live()) {
			t.Errorf("insecure: yeniden baslatma notu bekleniyordu:\n%s", s)
		}
	})

	t.Run("enable_terminal canli", func(t *testing.T) {
		isolateHome(t)
		reloads := 0
		out := stub(t, &localctl.Endpoint{Service: false}, nil, func(r localctl.Request) (localctl.Response, error) {
			if r.Op == "reload" {
				reloads++
			}
			return localctl.Response{OK: true}, nil
		})
		if err := mutateConfig(false, "enable_terminal", setKeyFn("enable_terminal", "true"), true); err != nil {
			t.Fatal(err)
		}
		s := out.String()
		if reloads != 1 || !strings.Contains(s, live()) || strings.Contains(s, later()) {
			t.Errorf("enable_terminal canli uygulanmali (reload=%d):\n%s", reloads, s)
		}
	})

	t.Run("kapsam eslesmiyor", func(t *testing.T) {
		isolateHome(t)
		// Servis (sistem dosyasi) calisiyor, kullanici dosyasi duzenleniyor: ne canli ne "later".
		out := stub(t, &localctl.Endpoint{Service: true}, nil, func(localctl.Request) (localctl.Response, error) {
			t.Error("kapsam eslesmiyorsa ajana istek gitmemeli")
			return localctl.Response{}, nil
		})
		for _, key := range []string{"enable_deploy", "enable_terminal"} {
			out.Reset()
			if err := mutateConfig(false, key, setKeyFn(key, "true"), true); err != nil {
				t.Fatal(err)
			}
			if s := out.String(); !strings.Contains(s, restart()) || strings.Contains(s, live()) {
				t.Errorf("%s: kapsam uyusmazligi notu bekleniyordu:\n%s", key, s)
			}
		}
	})

	t.Run("calisan ajan yok", func(t *testing.T) {
		isolateHome(t)
		out := stub(t, nil, nil, func(localctl.Request) (localctl.Response, error) {
			t.Error("ajan yokken istek gitmemeli")
			return localctl.Response{}, nil
		})
		for _, key := range []string{"enable_deploy", "enable_terminal"} {
			out.Reset()
			if err := mutateConfig(false, key, setKeyFn(key, "true"), true); err != nil {
				t.Fatal(err)
			}
			s := out.String()
			if strings.Contains(s, live()) || strings.Contains(s, later()) || strings.Contains(s, restart()) {
				t.Errorf("%s: ajan yokken canli/yeniden baslatma notu olmamali:\n%s", key, s)
			}
		}
	})
}
