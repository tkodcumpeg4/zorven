package api

import "testing"

func findDiff(t *testing.T, in []fieldDiff, path string) fieldDiff {
	t.Helper()
	for _, d := range in {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("%q yolu diff'te yok: %+v", path, in)
	return fieldDiff{}
}

func TestDiffBodiesJSONScalarChange(t *testing.T) {
	kind, out, _ := diffBodies([]byte(`{"status":"paid","amount":10}`), []byte(`{"status":"pending","amount":10}`))
	if kind != "json" {
		t.Fatalf("kind = %s, json bekleniyordu", kind)
	}
	if len(out) != 1 {
		t.Fatalf("1 fark bekleniyordu: %+v", out)
	}
	d := findDiff(t, out, "$.status")
	if d.Old != "paid" || d.New != "pending" || d.Kind != "changed" {
		t.Errorf("diff = %+v", d)
	}
}

// Alan sirasi degisince fark URETILMEMELI — JSON diff'in varlik sebebi bu.
func TestDiffBodiesJSONIgnoresKeyOrder(t *testing.T) {
	_, out, _ := diffBodies([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`))
	if len(out) != 0 {
		t.Errorf("sira farki fark sayilmamali: %+v", out)
	}
}

func TestDiffBodiesJSONAddedRemoved(t *testing.T) {
	_, out, _ := diffBodies([]byte(`{"a":1,"gone":true}`), []byte(`{"a":1,"fresh":"x"}`))
	if g := findDiff(t, out, "$.gone"); g.Kind != "removed" || g.Old != "true" {
		t.Errorf("gone = %+v", g)
	}
	if f := findDiff(t, out, "$.fresh"); f.Kind != "added" || f.New != "x" {
		t.Errorf("fresh = %+v", f)
	}
}

func TestDiffBodiesJSONNestedAndArrays(t *testing.T) {
	_, out, _ := diffBodies(
		[]byte(`{"items":[{"price":10},{"price":20}]}`),
		[]byte(`{"items":[{"price":10},{"price":25},{"price":5}]}`))
	if d := findDiff(t, out, "$.items[1].price"); d.Old != "20" || d.New != "25" {
		t.Errorf("fiyat degisimi = %+v", d)
	}
	if d := findDiff(t, out, "$.items[2]"); d.Kind != "added" {
		t.Errorf("yeni eleman = %+v", d)
	}
}

// Tam sayilar ".000000" kuyrugu olmadan gosterilmeli.
func TestDiffBodiesIntegerFormatting(t *testing.T) {
	_, out, _ := diffBodies([]byte(`{"n":1}`), []byte(`{"n":2}`))
	d := findDiff(t, out, "$.n")
	if d.Old != "1" || d.New != "2" {
		t.Errorf("sayi bicimi = %+v", d)
	}
}

func TestDiffBodiesFallsBackToText(t *testing.T) {
	kind, out, _ := diffBodies([]byte("satir bir\nsatir iki"), []byte("satir bir\nsatir UC"))
	if kind != "text" {
		t.Fatalf("kind = %s, text bekleniyordu", kind)
	}
	if len(out) != 1 || out[0].New != "satir UC" {
		t.Errorf("satir diff = %+v", out)
	}
}

func TestDiffHeadersIgnoresVolatile(t *testing.T) {
	old := map[string][]string{"Date": {"dun"}, "X-App": {"v1"}, "ETag": {"a"}}
	new := map[string][]string{"Date": {"bugun"}, "X-App": {"v2"}, "ETag": {"b"}}
	out := diffHeaders(old, new)
	if len(out) != 1 {
		t.Fatalf("yalnizca X-App fark sayilmali: %+v", out)
	}
	if out[0].Path != "x-app" || out[0].Old != "v1" || out[0].New != "v2" {
		t.Errorf("diff = %+v", out[0])
	}
}

func TestDiffHeadersAddedRemoved(t *testing.T) {
	out := diffHeaders(
		map[string][]string{"X-Gone": {"1"}},
		map[string][]string{"X-New": {"2"}})
	if g := findDiff(t, out, "x-gone"); g.Kind != "removed" {
		t.Errorf("x-gone = %+v", g)
	}
	if n := findDiff(t, out, "x-new"); n.Kind != "added" {
		t.Errorf("x-new = %+v", n)
	}
}

// Coklu deger sirasi fark sayilmamali.
func TestDiffHeadersMultiValueOrderIgnored(t *testing.T) {
	out := diffHeaders(
		map[string][]string{"Accept": {"a", "b"}},
		map[string][]string{"accept": {"b", "a"}})
	if len(out) != 0 {
		t.Errorf("sira farki fark sayilmamali: %+v", out)
	}
}

// Cok buyuk farklarda liste kirpilir ve bu ACIKCA bildirilir.
func TestDiffBodiesTruncates(t *testing.T) {
	oldB := []byte(`{`)
	newB := []byte(`{`)
	for i := 0; i < diffMaxEntries+50; i++ {
		if i > 0 {
			oldB = append(oldB, ',')
			newB = append(newB, ',')
		}
		oldB = append(oldB, []byte(`"k`+itoa(i)+`":1`)...)
		newB = append(newB, []byte(`"k`+itoa(i)+`":2`)...)
	}
	oldB = append(oldB, '}')
	newB = append(newB, '}')

	_, out, truncated := diffBodies(oldB, newB)
	if !truncated {
		t.Error("kirpma bildirilmeliydi")
	}
	if len(out) > diffMaxEntries {
		t.Errorf("%d kayit dondu, sinir %d", len(out), diffMaxEntries)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
