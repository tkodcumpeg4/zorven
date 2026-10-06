package agent

import "testing"

func TestRawHostPort(t *testing.T) {
	ok := map[string]string{
		"localhost:25565":        "localhost:25565",
		"tcp://localhost:25565":  "localhost:25565",
		"udp://127.0.0.1:19132":  "127.0.0.1:19132",
		"http://localhost:8000/": "localhost:8000",
		"https://10.0.0.5:8443":  "10.0.0.5:8443",
		"[::1]:25565":            "[::1]:25565",
	}
	for in, want := range ok {
		got, err := rawHostPort(in)
		if err != nil {
			t.Errorf("rawHostPort(%q) beklenmedik hata: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("rawHostPort(%q)=%q, beklenen %q", in, got, want)
		}
	}

	bad := []string{"", "localhost", "http://localhost", "://x", "justtext"}
	for _, in := range bad {
		if _, err := rawHostPort(in); err == nil {
			t.Errorf("rawHostPort(%q) hata dondurmeliydi", in)
		}
	}
}
