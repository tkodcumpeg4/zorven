package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fakeRunner: "pano" icerigini tutan sahte alt surec calistirici.
type fakeRunner struct {
	content   string
	empty     bool // arac hata koduyla cikar (metin yok)
	outErr    error
	inErr     error
	outCalls  int
	inCalls   int
	lastName  string
	lastArgs  []string
	delayedIn string // Input sonrasi Output bu eski degeri dondurur (arka plan sahiplenme gecikmesi)
}

func (f *fakeRunner) Output(_ context.Context, name string, args []string, limit int) ([]byte, error) {
	f.outCalls++
	f.lastName, f.lastArgs = name, args
	if f.outErr != nil {
		return nil, f.outErr
	}
	if f.empty {
		return nil, errClipCmdExit
	}
	b := []byte(f.content)
	if len(b) > limit {
		b = b[:limit]
	}
	return b, nil
}

func (f *fakeRunner) Input(_ context.Context, name string, args []string, data []byte) error {
	f.inCalls++
	f.lastName, f.lastArgs = name, args
	if f.inErr != nil {
		return f.inErr
	}
	if f.delayedIn == "" {
		f.content = string(data)
	}
	return nil
}

func newTestCmdClip(r *fakeRunner) (*cmdClipboard, *time.Time) {
	now := time.Unix(1000, 0)
	c := newCmdClipboard(r, clipTool{"fake", []string{"paste", "-x"}, []string{"copy", "-y"}})
	c.now = func() time.Time { return now }
	return c, &now
}

func TestCmdClipboardSeqByDigest(t *testing.T) {
	r := &fakeRunner{content: "ilk"}
	c, _ := newTestCmdClip(r)
	base := c.Seq()
	if c.Seq() != base {
		t.Fatal("ayni icerik: sayac sabit kalmali")
	}
	r.content = "ikinci"
	s2 := c.Seq()
	if s2 == base {
		t.Fatal("icerik degisti: sayac artmali")
	}
	if got, _ := c.Get(); got != "ikinci" {
		t.Fatalf("Get onbellekten donmeli: %q", got)
	}
	r.empty = true // arac "metin yok" diyor
	s3 := c.Seq()
	if s3 == s2 {
		t.Fatal("panonun bosalmasi da degisimdir")
	}
	if got, _ := c.Get(); got != "" {
		t.Fatalf("bos bekleniyor: %q", got)
	}
	// Okunamayan tur (zaman asimi): degisim yok sayilir, sayac ayni.
	r.empty, r.outErr = false, context.DeadlineExceeded
	if c.Seq() != s3 {
		t.Fatal("okuma hatasi sayaci degistirmemeli")
	}
	if r.lastName != "paste" || len(r.lastArgs) != 1 || r.lastArgs[0] != "-x" {
		t.Fatalf("komut: %s %v", r.lastName, r.lastArgs)
	}
}

func TestCmdClipboardSetHoldsAgainstStaleReadback(t *testing.T) {
	r := &fakeRunner{content: "eski", delayedIn: "evet"} // Input sonrasi hala "eski" okunur
	c, now := newTestCmdClip(r)
	c.Seq()
	if err := c.Set("yeni"); err != nil {
		t.Fatal(err)
	}
	seq := c.Seq()
	calls := r.outCalls
	if c.Seq() != seq || r.outCalls != calls {
		t.Fatal("bekleme suresi icinde geri okunmamali")
	}
	// Sure dolunca arac artik yeni icerigi dondurur: degisim sayilmamali.
	*now = now.Add(clipSetHold + time.Millisecond)
	r.content = "yeni"
	if c.Seq() != seq {
		t.Fatal("Set ettigimiz icerik degisim sayilmamali")
	}
	if r.lastName != "paste" {
		t.Fatal("sure dolunca yeniden okunmali")
	}
}

func TestCmdClipboardSetError(t *testing.T) {
	r := &fakeRunner{inErr: errors.New("xclip: bulunamadi")}
	c, _ := newTestCmdClip(r)
	if err := c.Set("x"); err == nil {
		t.Fatal("hata iletilmeli")
	}
}

func TestCmdClipboardWatcherIntegration(t *testing.T) {
	r := &fakeRunner{content: "baslangic"}
	c, now := newTestCmdClip(r)
	var sent []string
	var notes []string
	w := newClipboardWatcher(c, func(s string) { sent = append(sent, s) })
	w.notice = func(d string) { notes = append(notes, d) }
	w.SetEnabled(true)
	w.Step()
	if len(sent) != 0 {
		t.Fatal("acilista mevcut icerik sizmamali")
	}
	r.content = "kopyalandi"
	w.Step()
	if len(sent) != 1 || sent[0] != "kopyalandi" {
		t.Fatalf("%v", sent)
	}
	// Panelden gelen metin yansimaz.
	if err := w.Apply("paneldeki"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * clipSetHold)
	w.Step()
	if len(sent) != 1 {
		t.Fatalf("yazilan metin geri yansidi: %v", sent)
	}
	// Cok buyuk icerik: gonderilmez, kullaniciya bildirilir.
	r.content = strings.Repeat("a", protocol.MaxScreenClipboardSize+500)
	w.Step()
	if len(sent) != 1 || len(notes) != 1 || notes[0] != protocol.DegradedClipboardTooLarge {
		t.Fatalf("sent=%d notes=%v", len(sent), notes)
	}
}

func TestCmdClipboardPollInterval(t *testing.T) {
	c, _ := newTestCmdClip(&fakeRunner{})
	var be clipboardBackend = c
	p, ok := be.(pollIntervaler)
	if !ok || p.pollInterval() < time.Second {
		t.Fatal("Linux/macOS'ta yoklama araligi >= 1 sn olmali")
	}
}

func TestDetectClipTool(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	have := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, x := range names {
				if x == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("yok")
		}
	}
	cases := []struct {
		name string
		goos string
		env  map[string]string
		bins []string
		want string // "" => nil
	}{
		{"mac", "darwin", nil, []string{"pbcopy", "pbpaste"}, "pbcopy"},
		{"mac eksik", "darwin", nil, []string{"pbcopy"}, ""},
		{"wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"wl-copy", "wl-paste", "xclip"}, "wl-clipboard"},
		{"wayland arac yok x11'e duser", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"xclip"}, "xclip"},
		{"x11 xclip", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xclip", "xsel"}, "xclip"},
		{"x11 xsel", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, "xsel"},
		{"ekran yok (servis)", "linux", nil, []string{"xclip", "wl-copy", "wl-paste"}, ""},
		{"arac yok", "linux", map[string]string{"DISPLAY": ":0"}, nil, ""},
		{"windows buraya girmez", "windows", map[string]string{"DISPLAY": ":0"}, []string{"xclip"}, ""},
	}
	for _, c := range cases {
		got := detectClipTool(c.goos, env(c.env), have(c.bins...))
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%s: nil beklenirdi, %v", c.name, got.name)
		case c.want != "" && (got == nil || got.name != c.want):
			t.Errorf("%s: %s beklenirdi", c.name, c.want)
		}
	}
}
