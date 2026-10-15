package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFileFrameRoundTrip(t *testing.T) {
	in := BodyFrame{FrameType: FrameFile, ReqID: 77, Seq: 9, EOF: true, Payload: []byte("abc")}
	out, err := DecodeBodyFrame(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.FrameType != FrameFile || out.ReqID != 77 || out.Seq != 9 || !out.EOF || string(out.Payload) != "abc" {
		t.Fatalf("bozuk: %+v", out)
	}
}

func TestFileMessagesRoundTrip(t *testing.T) {
	cases := []struct {
		v any
		t MessageType
	}{
		{FileOpen{Type: TypeFileOpen, XferID: 1, Dir: FileDirPut, Name: "a.txt", Size: 5}, TypeFileOpen},
		{FileOpenAck{Type: TypeFileOpenAck, XferID: 1, OK: true, Name: "a (1).txt"}, TypeFileOpenAck},
		{FileAck{Type: TypeFileAck, XferID: 1, Bytes: 99}, TypeFileAck},
		{FileEnd{Type: TypeFileEnd, XferID: 1, Size: 5, SHA256: "ab"}, TypeFileEnd},
		{FileDone{Type: TypeFileDone, XferID: 1, OK: false, Code: FileErrChecksum}, TypeFileDone},
		{FileAbort{Type: TypeFileAbort, XferID: 1, Code: FileErrCanceled}, TypeFileAbort},
		{FileList{Type: TypeFileList, ReqID: 3}, TypeFileList},
		{FileListResult{Type: TypeFileListResult, ReqID: 3, Dir: "/x", Files: []FileInfo{{Name: "a", Size: 1, ModTime: 2}}}, TypeFileListResult},
	}
	for _, c := range cases {
		b, err := Marshal(c.v, c.t)
		if err != nil {
			t.Fatalf("%s: %v", c.t, err)
		}
		if got, _ := PeekType(b); got != c.t {
			t.Fatalf("tip %s != %s", got, c.t)
		}
	}
	var o FileOpen
	b, _ := Marshal(cases[0].v, TypeFileOpen)
	if json.Unmarshal(b, &o) != nil || o.Name != "a.txt" || o.Dir != FileDirPut || o.Size != 5 {
		t.Fatalf("open: %+v", o)
	}
}

func TestSanitizeFileName(t *testing.T) {
	ok := map[string]string{
		"rapor.pdf":          "rapor.pdf",
		"Türkçe şğüöç ı.txt": "Türkçe şğüöç ı.txt",
		"日本語ファイル.docx":       "日本語ファイル.docx",
		"a:b*c?.txt":         "a_b_c_.txt",
		"trailing. . ":       "trailing",
		"CON":                "_CON",
		"con.txt":            "_con.txt",
		"NUL.tar.gz":         "_NUL.tar.gz",
		"COM1":               "_COM1",
		"lpt9.log":           "_lpt9.log",
		"COM0":               "COM0",
		"CONSOLE.txt":        "CONSOLE.txt",
		"aux ":               "_aux",
		".hidden":            ".hidden",
	}
	for in, want := range ok {
		got, err := SanitizeFileName(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), beklenen %q", in, got, err, want)
		}
	}
	bad := []string{"", ".", "..", "../etc/passwd", "a/b", `a\b`, `..\..\win.ini`, "/abs", `C:\x`, "a\x00b", "a\nb", "...", " . ", "\xff\xfe"}
	for _, in := range bad {
		if got, err := SanitizeFileName(in); err == nil {
			t.Errorf("%q kabul edildi -> %q", in, got)
		}
	}
}

func TestSanitizeFileNameLong(t *testing.T) {
	long := strings.Repeat("ğ", 300) + ".tar.gz"
	got, err := SanitizeFileName(long)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxFileNameBytes || !strings.HasSuffix(got, ".gz") {
		t.Fatalf("uzunluk %d, ad %q", len(got), got[len(got)-8:])
	}
	if !strings.ContainsRune(got, 'ğ') || strings.ContainsRune(got, '\ufffd') {
		t.Fatal("rune siniri bozuldu")
	}
}

func TestValidExistingFileName(t *testing.T) {
	for _, n := range []string{"a.txt", "CON", "x y (1).zip", "ğ"} {
		if ValidExistingFileName(n) != nil {
			t.Errorf("%q reddedildi", n)
		}
	}
	for _, n := range []string{"", "..", ".", "a/b", `a\b`, "a\x00", "../x"} {
		if ValidExistingFileName(n) == nil {
			t.Errorf("%q kabul edildi", n)
		}
	}
}

func TestUniqueFileName(t *testing.T) {
	cases := map[[2]any]string{
		{"a.txt", 0}:    "a.txt",
		{"a.txt", 1}:    "a (1).txt",
		{"a.tar.gz", 2}: "a.tar (2).gz",
		{"noext", 3}:    "noext (3)",
		{".hidden", 1}:  ".hidden (1)",
	}
	for k, want := range cases {
		if got := UniqueFileName(k[0].(string), k[1].(int)); got != want {
			t.Errorf("%v -> %q, beklenen %q", k, got, want)
		}
	}
}

func TestFileCreditBackpressure(t *testing.T) {
	c := NewFileCredit(100)
	ctx := context.Background()
	if err := c.Reserve(ctx, 60); err != nil {
		t.Fatal(err)
	}
	if err := c.Reserve(ctx, 40); err != nil {
		t.Fatal(err)
	}
	// Pencere dolu: ucuncu rezervasyon ack gelene kadar bekler.
	done := make(chan error, 1)
	go func() { done <- c.Reserve(ctx, 30) }()
	select {
	case <-done:
		t.Fatal("kredi yokken gecti")
	case <-time.After(50 * time.Millisecond):
	}
	if !c.Ack(60) {
		t.Fatal("ack reddedildi")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ack sonrasi acilmadi")
	}
	if c.InFlight() != 70 {
		t.Fatalf("ucusta %d", c.InFlight())
	}
	// Gonderilenden fazla ack ve geri giden ack protokol ihlalidir.
	if c.Ack(1000) || c.Ack(10) {
		t.Fatal("gecersiz ack kabul edildi")
	}
}

func TestFileCreditCancelAndFail(t *testing.T) {
	c := NewFileCredit(10)
	_ = c.Reserve(context.Background(), 10)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := c.Reserve(ctx, 5); err == nil {
		t.Fatal("iptal hata vermedi")
	}
	c2 := NewFileCredit(10)
	_ = c2.Reserve(context.Background(), 10)
	go func() { time.Sleep(20 * time.Millisecond); c2.Fail(context.DeadlineExceeded) }()
	if err := c2.Reserve(context.Background(), 5); err == nil {
		t.Fatal("Fail hata vermedi")
	}
}

func TestFileCreditOversizedChunkPasses(t *testing.T) {
	c := NewFileCredit(10)
	if err := c.Reserve(context.Background(), 50); err != nil {
		t.Fatal(err) // ucusta bayt yokken pencereden buyuk parca takilmamali
	}
}

func TestFileAcker(t *testing.T) {
	var a FileAcker
	n := 0
	for i := 0; i < 20; i++ {
		if tot, ok := a.Consume(FileChunkSize); ok {
			n++
			if tot != a.Total() {
				t.Fatal("kumulatif tutarsiz")
			}
		}
	}
	if n != 2 { // 20*32K = 640K -> 256K'da ve 512K'da
		t.Fatalf("ack sayisi %d", n)
	}
}

func TestFileRateThrottles(t *testing.T) {
	var slept time.Duration
	clock := time.Unix(1000, 0)
	r := NewFileRate(1000)
	r.now = func() time.Time { return clock }
	r.sleep = func(_ context.Context, d time.Duration) error { slept += d; clock = clock.Add(d); return nil }
	for i := 0; i < 10; i++ {
		if err := r.Wait(context.Background(), 500); err != nil {
			t.Fatal(err)
		}
	}
	// 5000 bayt @1000 B/sn ~ 5 sn (ilk 125 B patlama payi dusulur).
	if slept < 4500*time.Millisecond || slept > 5*time.Second {
		t.Fatalf("beklenen ~4.9s, uyunan %v", slept)
	}
}
