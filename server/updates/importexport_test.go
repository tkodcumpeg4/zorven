package updates

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestParseImportShapes(t *testing.T) {
	one := `{"slug":"a","title_tr":"A","body_tr":"x"}`
	for name, in := range map[string]string{
		"tek":    one,
		"dizi":   "[" + one + "," + one + "]",
		"zarf":   `{"posts":[` + one + `]}`,
		"bom":    "\xef\xbb\xbf" + one,
		"bosluk": "  \n" + one + "\n",
	} {
		items, err := ParseImport([]byte(in))
		if err != nil || len(items) == 0 || items[0].Err != "" {
			t.Fatalf("%s: %v %+v", name, err, items)
		}
	}
}

func TestParseImportStrict(t *testing.T) {
	items, err := ParseImport([]byte(`[{"slug":"a","zzz":1,"aaa":2},{"slug":"b","published":"evet"},{"slug":"c","maintenance_start":"yarin"},5]`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(items[0].Err, "aaa, zzz") {
		t.Fatalf("bilinmeyen alanlar listelenmedi: %q", items[0].Err)
	}
	for i := 1; i < 4; i++ {
		if items[i].Err == "" {
			t.Fatalf("oge %d hatasiz: %+v", i, items[i])
		}
	}
}

func TestImportExampleFileIsValid(t *testing.T) {
	data, err := os.ReadFile("../../docs/updates-import-example.json")
	if os.IsNotExist(err) {
		t.Skip("ornek dosya yok (acik surumde docs/ aktarilmaz)")
	}
	if err != nil {
		t.Fatal(err)
	}
	items, err := ParseImport(data)
	if err != nil || len(items) != 1 || items[0].Err != "" {
		t.Fatalf("ornek gecersiz: %v %+v", err, items)
	}
	p := items[0].Post
	if p.Published || p.BodyTR == "" || p.BodyEN == "" || p.BodyJA == "" || p.Slug != "zorven-0-2-15" {
		t.Fatalf("ornek icerigi: %+v", p)
	}
}

func TestParseImportErrorsAndCaps(t *testing.T) {
	for name, in := range map[string]string{
		"bos":       "",
		"sayi":      "42",
		"bozuk":     "{",
		"bos dizi":  "[]",
		"zarf+alan": `{"posts":[],"x":1}`,
		"posts obj": `{"posts":{}}`,
	} {
		if _, err := ParseImport([]byte(in)); err == nil {
			t.Errorf("%s: hata beklenirdi", name)
		}
	}
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < MaxImportPosts+1; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"slug":"s%d"}`, i)
	}
	sb.WriteString("]")
	if _, err := ParseImport([]byte(sb.String())); err == nil {
		t.Error("200 yazi siniri uygulanmadi")
	}
	big := `{"slug":"a","body_tr":"` + strings.Repeat("x", MaxImportBytes) + `"}`
	if _, err := ParseImport([]byte(big)); err == nil {
		t.Error("boyut siniri uygulanmadi")
	}
}
