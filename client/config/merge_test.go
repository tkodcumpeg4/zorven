package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// desktopLike, masaustu uygulamasinin yapisini taklit eder: CLI'nin bilmedigi alanlar.
type desktopLike struct {
	ServerAddr  string `json:"server_addr"`
	Token       string `json:"token"`
	AutoConnect bool   `json:"auto_connect"`
	NoTerminal  bool   `json:"no_terminal"`
	TokenEnc    string `json:"token_enc,omitempty"`
	Theme       string `json:"theme,omitempty"`
	RecentPorts []int  `json:"recent_ports,omitempty"`
	Hidden      string `json:"-"`
	unexported  int    //nolint:unused
}

// fieldsOf, JSON nesnesini anahtar -> sikistirilmis ham deger sozlugune cevirir.
func fieldsOf(t *testing.T, data []byte) map[string]string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("gecerli JSON degil: %v\n%s", err, data)
	}
	out := map[string]string{}
	for k, v := range m {
		var b bytes.Buffer
		if err := json.Compact(&b, v); err != nil {
			t.Fatal(err)
		}
		out[k] = b.String()
	}
	return out
}

// keyOrder, JSON nesnesinin ust duzey anahtarlarini dosyadaki sirayla verir.
func keyOrder(t *testing.T, data []byte) []string {
	t.Helper()
	f, err := parseObject(data)
	if err != nil {
		t.Fatalf("parseObject: %v\n%s", err, data)
	}
	var ks []string
	for _, x := range f {
		ks = append(ks, x.key)
	}
	return ks
}

func TestJSONKeys(t *testing.T) {
	got := jsonKeys(Config{})
	want := []string{"server_addr", "servers", "server_pinned", "token", "local_url", "ca_cert_path", "insecure", "enable_terminal", "enable_screen", "enable_deploy", "operators", "language"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Config anahtarlari:\n got %v\nwant %v", got, want)
	}
	// Isaretci, "-" ve disa kapali alanlar.
	got = jsonKeys(&desktopLike{})
	want = []string{"server_addr", "token", "auto_connect", "no_terminal", "token_enc", "theme", "recent_ports"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desktopLike anahtarlari:\n got %v\nwant %v", got, want)
	}
	// Etiketsiz gomulu yapi duzlestirilir; etiketsiz alan Go adiyla yazilir.
	type inner struct {
		A int `json:"a"`
	}
	type outer struct {
		inner
		B string
		C int `json:",omitempty"`
	}
	if got = jsonKeys(outer{}); !reflect.DeepEqual(got, []string{"a", "B", "C"}) {
		t.Fatalf("gomulu yapi: %v", got)
	}
	if jsonKeys(42) != nil || jsonKeys(nil) != nil {
		t.Fatal("yapi olmayan deger anahtar uretmemeli")
	}
}

// CLI yazimi masaustu alanlarini ve gelecekteki bilinmeyen alani korur.
func TestMergeJSONPreservesUnknownFields(t *testing.T) {
	existing := `{
  "server_addr": "eski:443",
  "token": "",
  "auto_connect": true,
  "no_terminal": true,
  "token_enc": "dpapi:AAEC",
  "theme": "dark",
  "recent_ports": [3000, 5173],
  "gelecek_alan": {"ic": [1, 2, {"x": null}], "buyuk": 12345678901234567890, "kesir": 1.50},
  "gelecek_liste": ["a", "b"]
}`
	res, err := MergeJSON([]byte(existing), Config{ServerAddr: "yeni:443", Token: "zrv_live_t", EnableTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrupt {
		t.Fatal("gecerli dosya bozuk sayildi")
	}
	f := fieldsOf(t, res.Data)
	for k, want := range map[string]string{
		"auto_connect": "true", "no_terminal": "true", "token_enc": `"dpapi:AAEC"`, "theme": `"dark"`,
		"recent_ports":  "[3000,5173]",
		"gelecek_alan":  `{"ic":[1,2,{"x":null}],"buyuk":12345678901234567890,"kesir":1.50}`,
		"gelecek_liste": `["a","b"]`,
	} {
		if f[k] != want {
			t.Errorf("%s korunmadi: %q, beklenen %q", k, f[k], want)
		}
	}
	// Bilinen alanlar yapidan guncellenir.
	if f["server_addr"] != `"yeni:443"` || f["token"] != `"zrv_live_t"` || f["enable_terminal"] != "true" {
		t.Errorf("bilinen alanlar guncellenmedi: %v", f)
	}
	if got := strings.Join(res.Preserved, ","); got != "auto_connect,no_terminal,token_enc,theme,recent_ports,gelecek_alan,gelecek_liste" {
		t.Errorf("korunan listesi: %s", got)
	}
}

// Masaustu yazimi CLI alanlarini korur (ayni yardimci, tersi yon).
func TestMergeJSONDesktopSideKeepsCLIFields(t *testing.T) {
	existing := `{"server_addr":"a:443","token":"zrv_live_x","enable_terminal":true,"enable_screen":true,"enable_deploy":true,"language":"ja","ileride":"x"}`
	res, err := MergeJSON([]byte(existing), desktopLike{ServerAddr: "a:443", Token: "", TokenEnc: "dpapi:ZZ", AutoConnect: true})
	if err != nil {
		t.Fatal(err)
	}
	f := fieldsOf(t, res.Data)
	for k, want := range map[string]string{
		"enable_terminal": "true", "enable_screen": "true", "enable_deploy": "true", "language": `"ja"`, "ileride": `"x"`,
		"token_enc": `"dpapi:ZZ"`, "auto_connect": "true", "token": `""`,
	} {
		if f[k] != want {
			t.Errorf("%s: %q, beklenen %q", k, f[k], want)
		}
	}
}

// Yapinin bildigi ama bos oldugu icin yazmadigi (omitempty) anahtar dosyadan silinir:
// aksi halde `config unset` / bir ayari kapatma calismazdi. Bilinmeyenler kalir.
func TestMergeJSONDropsEmptiedKnownKeys(t *testing.T) {
	existing := `{"server_addr":"a:443","token":"t","enable_deploy":true,"ca_cert_path":"/x.pem","insecure":true,"language":"ja","local_url":"http://localhost:9999","token_enc":"dpapi:Q","auto_connect":true}`
	res, err := MergeJSON([]byte(existing), Config{ServerAddr: "a:443", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	f := fieldsOf(t, res.Data)
	for _, k := range []string{"enable_deploy", "ca_cert_path", "insecure", "language", "local_url"} {
		if _, ok := f[k]; ok {
			t.Errorf("bos yazilan bilinen anahtar %s dosyadan silinmeliydi: %v", k, f)
		}
	}
	if f["token_enc"] != `"dpapi:Q"` || f["auto_connect"] != "true" {
		t.Errorf("bilinmeyen alanlar silindi: %v", f)
	}
}

func TestMergeJSONKeepsOrderAndAppendsNewKeys(t *testing.T) {
	existing := `{"zeta":1,"server_addr":"a:443","alfa":2,"token":"t","omega":3}`
	res, err := MergeJSON([]byte(existing), Config{ServerAddr: "b:443", Token: "t2", EnableDeploy: true, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zeta", "server_addr", "alfa", "token", "omega", "enable_deploy", "language"}
	if got := keyOrder(t, res.Data); !reflect.DeepEqual(got, want) {
		t.Fatalf("anahtar sirasi:\n got %v\nwant %v", got, want)
	}
}

// Dosya yoksa (ya da bos/bosluksa) sonuc eski json.MarshalIndent ile birebir ayni.
func TestMergeJSONFreshEqualsMarshalIndent(t *testing.T) {
	cfgs := []Config{
		DefaultConfig(),
		{ServerAddr: "a:1", Token: "zrv_live_<&>", EnableTerminal: true, EnableScreen: true, EnableDeploy: true, Language: "zh", CACertPath: `C:\ca.pem`, Insecure: true},
		{},
	}
	for i, cfg := range cfgs {
		want, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for _, existing := range [][]byte{nil, {}, []byte("  \n\t ")} {
			res, err := MergeJSON(existing, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if res.Corrupt || !bytes.Equal(res.Data, want) {
				t.Errorf("[%d] bos dosyada cikti degisti (corrupt=%v):\n got %s\nwant %s", i, res.Corrupt, res.Data, want)
			}
		}
	}
}

func TestMergeJSONCorruptFallsBackToStructOnly(t *testing.T) {
	want, _ := json.MarshalIndent(Config{ServerAddr: "a:1", Token: "t"}, "", "  ")
	for _, bad := range []string{`{bozuk`, `[1,2]`, `"metin"`, `null`, `42`, `{"a":1} fazlalik`, `{"a":1}{"b":2}`, `{"a":`, "\ufeff{}"} {
		res, err := MergeJSON([]byte(bad), Config{ServerAddr: "a:1", Token: "t"})
		if err != nil {
			t.Errorf("%q: hata dondu: %v", bad, err)
			continue
		}
		if !res.Corrupt || !bytes.Equal(res.Data, want) || len(res.Preserved) != 0 {
			t.Errorf("%q: Corrupt=%v preserved=%v data=%s", bad, res.Corrupt, res.Preserved, res.Data)
		}
	}
}

// Tekrar eden bilinen anahtar tek kalir; buyuk/kucuk harf farkli yazilmis eski anahtar
// (encoding/json yuklerken esler) bayat deger olarak kalmaz.
func TestMergeJSONDuplicateAndCaseVariants(t *testing.T) {
	res, err := MergeJSON([]byte(`{"token":"a","Token":"b","SERVER_ADDR":"x","token":"c","not":1,"not":2}`), Config{ServerAddr: "s:1", Token: "yeni"})
	if err != nil {
		t.Fatal(err)
	}
	ks := keyOrder(t, res.Data)
	count := map[string]int{}
	for _, k := range ks {
		count[k]++
	}
	if count["token"] != 1 || count["server_addr"] != 1 || count["Token"] != 0 || count["SERVER_ADDR"] != 0 {
		t.Fatalf("bilinen anahtarlar tek ve kucuk harf olmali: %v", ks)
	}
	// Bilinmeyen tekrarlar oldugu gibi korunur.
	if count["not"] != 2 {
		t.Fatalf("bilinmeyen tekrarlar korunmali: %v", ks)
	}
	var c Config
	if err := json.Unmarshal(res.Data, &c); err != nil || c.Token != "yeni" || c.ServerAddr != "s:1" {
		t.Fatalf("yukleyici yeni degeri gormeli: %+v %v", c, err)
	}
}

func TestMergeJSONRejectsNonStruct(t *testing.T) {
	if _, err := MergeJSON(nil, []int{1}); err == nil {
		t.Error("nesne olmayan deger reddedilmeli")
	}
}

// SaveFile: masaustunun yazdigi dosyada CLI kaydi masaustu alanlarini korur, izinler
// daralir, gecici dosya kalmaz, LoadFile ayni sonucu okur.
func TestSaveFilePreservesDesktopFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "zorven", "config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	desktop := `{
  "server_addr": "zorven.app:443",
  "token": "",
  "auto_connect": true,
  "no_terminal": false,
  "no_screen": true,
  "hide_to_tray": true,
  "token_enc": "dpapi:SEALED",
  "theme": "light",
  "recent_ports": [3000],
  "gelecek": {"a": [true, null]}
}`
	if err := os.WriteFile(p, []byte(desktop), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("enable_deploy", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("enable_terminal", "on"); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	f := fieldsOf(t, data)
	for k, want := range map[string]string{
		"auto_connect": "true", "no_screen": "true", "hide_to_tray": "true", "token_enc": `"dpapi:SEALED"`,
		"theme": `"light"`, "recent_ports": "[3000]", "gelecek": `{"a":[true,null]}`, "no_terminal": "false",
		"enable_deploy": "true", "enable_terminal": "true",
	} {
		if f[k] != want {
			t.Errorf("%s: %q, beklenen %q\n%s", k, f[k], want, data)
		}
	}
	back, err := LoadFile(p)
	if err != nil || !back.EnableDeploy || !back.EnableTerminal || back.ServerAddr != "zorven.app:443" {
		t.Fatalf("yeniden okuma: %+v %v", back, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp-*")); len(left) != 0 {
		t.Fatalf("gecici dosya kaldi: %v", left)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("izin 0600 olmali: %v", fi.Mode())
		}
	}

	// Ayari kapatmak (unset) anahtari siler, masaustu alanlari yine kalir.
	if err := cfg.Unset("enable_deploy"); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(p)
	f = fieldsOf(t, data)
	if _, ok := f["enable_deploy"]; ok {
		t.Errorf("enable_deploy silinmeliydi:\n%s", data)
	}
	if f["token_enc"] != `"dpapi:SEALED"` || f["auto_connect"] != "true" || f["enable_terminal"] != "true" {
		t.Errorf("masaustu/diger alanlar bozuldu:\n%s", data)
	}
}

func TestSaveFileMissingAndEmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Hic yok: dizin ve dosya olusur, eski bicimle ayni icerik.
	p := filepath.Join(dir, "yeni", "config.json")
	if err := SaveFile(p, Config{ServerAddr: "a:1", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(Config{ServerAddr: "a:1", Token: "t"}, "", "  ")
	if got, _ := os.ReadFile(p); !bytes.Equal(got, want) {
		t.Fatalf("yeni dosya:\n got %s\nwant %s", got, want)
	}
	// Bos dosya: sorunsuz yazilir, yedek acilmaz.
	e := filepath.Join(dir, "bos.json")
	if err := os.WriteFile(e, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(e, Config{ServerAddr: "a:1", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(e); !bytes.Equal(got, want) {
		t.Fatalf("bos dosya:\n got %s", got)
	}
	if _, err := os.Stat(e + ".bak"); err == nil {
		t.Error("bos dosya icin yedek acilmamali")
	}
}

// Bozuk dosya yazmayi engellemez; ezilmeden once yedeklenir; ayni icerik tekrar
// yedeklenmez, farkli bozuk icerik eski yedegi ezmez.
func TestSaveFileCorruptIsBackedUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	bad := []byte(`{"token": "zrv_live_kurtar", "auto_connect": tr`)
	if err := os.WriteFile(p, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(p, Config{ServerAddr: "a:1", Token: "yeni"}); err != nil {
		t.Fatalf("bozuk dosya yazmayi engellememeli: %v", err)
	}
	if got, err := os.ReadFile(p + ".bak"); err != nil || !bytes.Equal(got, bad) {
		t.Fatalf("yedek: %v %q", err, got)
	}
	if c, err := LoadFile(p); err != nil || c.Token != "yeni" {
		t.Fatalf("yeni dosya gecerli olmali: %+v %v", c, err)
	}

	// Load'un ayni icerigi zaten yedekledigi akis: ikinci yedek acilmaz.
	if err := os.WriteFile(p, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(p, Config{ServerAddr: "a:1", Token: "yeni2"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(p + ".*.bak"); len(m) != 0 {
		t.Fatalf("ayni icerik icin ikinci yedek acilmamali: %v", m)
	}

	// Baska bozuk icerik: eski yedek korunur, yeni icerik zaman damgali yedege gider.
	bad2 := []byte(`{baska bozuk`)
	if err := os.WriteFile(p, bad2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(p, Config{ServerAddr: "a:1", Token: "yeni3"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p + ".bak"); !bytes.Equal(got, bad) {
		t.Fatalf("eski yedek ezilmemeli: %q", got)
	}
	m, _ := filepath.Glob(p + ".*.bak")
	if len(m) != 1 {
		t.Fatalf("zaman damgali yedek beklenirdi: %v", m)
	}
	if got, _ := os.ReadFile(m[0]); !bytes.Equal(got, bad2) {
		t.Fatalf("zaman damgali yedek icerigi: %q", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p + ".bak"); fi.Mode().Perm() != 0o600 {
			t.Fatalf("yedek izni 0600 olmali: %v", fi.Mode())
		}
	}
}

// MarshalMerged: okunamayan yol (dizin) ve cok buyuk dosya guvenli ele alinir.
func TestMarshalMergedUnreadableAndHuge(t *testing.T) {
	dir := t.TempDir()
	res, err := MarshalMerged(dir, Config{ServerAddr: "a:1"}) // dizin: okunamaz
	if err != nil || res.Corrupt || len(res.Data) == 0 {
		t.Fatalf("dizin yolu: %+v %v", res, err)
	}
	huge := filepath.Join(dir, "buyuk.json")
	if err := os.WriteFile(huge, append([]byte(`{"x":"`), bytes.Repeat([]byte("a"), maxConfigBytes+10)...), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = MarshalMerged(huge, Config{ServerAddr: "a:1"})
	if err != nil || !res.Corrupt || len(res.Preserved) != 0 {
		t.Fatalf("cok buyuk dosya bozuk sayilmali: %+v %v", res, err)
	}
	if _, err := os.Stat(huge + ".bak"); err == nil {
		t.Error("yedek acilmamis olmali (SaveFile cagrilmadi)")
	}
}
