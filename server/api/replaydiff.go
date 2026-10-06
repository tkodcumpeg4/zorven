package api

// FAZ 2 / F09 — Orijinal istek ile replay yanitinin karsilastirilmasi.
//
// Amac: "ayni istegi tekrar gonderdim, ne degisti?" sorusunu tek bakista
// cevaplamak. Karsilastirma SUNUCUDA yapilir cunku orijinal govde panele
// kirpilmis gidebilir; kirpilmis iki metni karsilastirmak yaniltici olurdu.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// diffMaxEntries, tek bir diff listesinde donulecek azami kayit.
// Sinir var cunku iki buyuk JSON arasindaki fark panelde okunamaz boyuta
// ulasabilir; ilk N fark zaten hikayeyi anlatir.
const diffMaxEntries = 200

// fieldDiff, tek bir alanin degisimi.
type fieldDiff struct {
	Path string `json:"path"`          // "status" veya "$.items[0].price"
	Old  string `json:"old,omitempty"` // bos = alan yeni eklendi
	New  string `json:"new,omitempty"` // bos = alan kaldirildi
	Kind string `json:"kind"`          // added | removed | changed
}

// replayDiff, panele donen karsilastirma ozeti.
type replayDiff struct {
	StatusChanged bool        `json:"status_changed"`
	OldStatus     int         `json:"old_status"`
	NewStatus     int         `json:"new_status"`
	OldDurationMS int64       `json:"old_duration_ms"`
	NewDurationMS int64       `json:"new_duration_ms"`
	Headers       []fieldDiff `json:"headers"`
	// BodyKind: json | text — govde diff'inin nasil uretildigi.
	BodyKind  string      `json:"body_kind"`
	Body      []fieldDiff `json:"body"`
	Truncated bool        `json:"truncated"`
}

// diffHeaders, iki basli kumesini karsilastirir. Coklu degerler virgulle
// birlestirilir: panelde tek satir gosterilecek, sira farki gurultu yapmasin
// diye degerler siralanir.
func diffHeaders(oldH, newH map[string][]string) []fieldDiff {
	keys := map[string]struct{}{}
	for k := range oldH {
		keys[strings.ToLower(k)] = struct{}{}
	}
	for k := range newH {
		keys[strings.ToLower(k)] = struct{}{}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	var out []fieldDiff
	for _, name := range names {
		o := joinHeader(lookupHeader(oldH, name))
		n := joinHeader(lookupHeader(newH, name))
		if o == n {
			continue
		}
		// Her istekte degisen basliklar fark sayilmaz: bunlari listelemek
		// gercek farklari gurultunun altinda birakirdi.
		if isVolatileHeader(name) {
			continue
		}
		out = append(out, fieldDiff{Path: name, Old: o, New: n, Kind: diffKind(o, n)})
		if len(out) >= diffMaxEntries {
			break
		}
	}
	return out
}

// isVolatileHeader, her yanitta dogal olarak degisen basliklar.
func isVolatileHeader(lower string) bool {
	switch lower {
	case "date", "age", "expires", "set-cookie", "etag", "last-modified",
		"content-length", "x-request-id", "x-trace-id", "cf-ray":
		return true
	}
	return false
}

func lookupHeader(h map[string][]string, lower string) []string {
	for k, v := range h {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return nil
}

func joinHeader(v []string) string {
	if len(v) == 0 {
		return ""
	}
	cp := append([]string(nil), v...)
	sort.Strings(cp)
	return strings.Join(cp, ", ")
}

func diffKind(old, new string) string {
	switch {
	case old == "":
		return "added"
	case new == "":
		return "removed"
	default:
		return "changed"
	}
}

// diffBodies, iki govdeyi karsilastirir. Ikisi de gecerli JSON ise JSON-path
// bazli; degilse satir bazli. JSON tercih edilir cunku alan sirasi degisen
// ama icerigi ayni olan iki cevap satir diff'inde tamamen farkli gorunur.
func diffBodies(oldB, newB []byte) (kind string, out []fieldDiff, truncated bool) {
	var ov, nv any
	if json.Unmarshal(oldB, &ov) == nil && json.Unmarshal(newB, &nv) == nil {
		out = diffJSON("$", ov, nv, nil)
		if len(out) > diffMaxEntries {
			out = out[:diffMaxEntries]
			truncated = true
		}
		return "json", out, truncated
	}
	out = diffLines(string(oldB), string(newB))
	if len(out) > diffMaxEntries {
		out = out[:diffMaxEntries]
		truncated = true
	}
	return "text", out, truncated
}

// diffJSON, iki cozulmus JSON degerini ozyinelemeli karsilastirir.
func diffJSON(path string, oldV, newV any, acc []fieldDiff) []fieldDiff {
	if len(acc) > diffMaxEntries {
		return acc
	}
	om, oIsMap := oldV.(map[string]any)
	nm, nIsMap := newV.(map[string]any)
	if oIsMap && nIsMap {
		keys := map[string]struct{}{}
		for k := range om {
			keys[k] = struct{}{}
		}
		for k := range nm {
			keys[k] = struct{}{}
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			ov, oOK := om[k]
			nv, nOK := nm[k]
			sub := path + "." + k
			switch {
			case oOK && !nOK:
				acc = append(acc, fieldDiff{Path: sub, Old: scalar(ov), Kind: "removed"})
			case !oOK && nOK:
				acc = append(acc, fieldDiff{Path: sub, New: scalar(nv), Kind: "added"})
			default:
				acc = diffJSON(sub, ov, nv, acc)
			}
		}
		return acc
	}

	oa, oIsArr := oldV.([]any)
	na, nIsArr := newV.([]any)
	if oIsArr && nIsArr {
		maxLen := len(oa)
		if len(na) > maxLen {
			maxLen = len(na)
		}
		for i := 0; i < maxLen; i++ {
			sub := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(na):
				acc = append(acc, fieldDiff{Path: sub, Old: scalar(oa[i]), Kind: "removed"})
			case i >= len(oa):
				acc = append(acc, fieldDiff{Path: sub, New: scalar(na[i]), Kind: "added"})
			default:
				acc = diffJSON(sub, oa[i], na[i], acc)
			}
		}
		return acc
	}

	if scalar(oldV) != scalar(newV) {
		acc = append(acc, fieldDiff{Path: path, Old: scalar(oldV), New: scalar(newV), Kind: "changed"})
	}
	return acc
}

// scalar, bir JSON degerini tek satirlik gosterime cevirir.
func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		// Tam sayilar ".000000" kuyrugu olmadan gosterilsin.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// diffLines, JSON olmayan govdeler icin satir bazli fark.
func diffLines(oldS, newS string) []fieldDiff {
	oldLines := strings.Split(oldS, "\n")
	newLines := strings.Split(newS, "\n")
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	var out []fieldDiff
	for i := 0; i < maxLen; i++ {
		var o, n string
		if i < len(oldLines) {
			o = oldLines[i]
		}
		if i < len(newLines) {
			n = newLines[i]
		}
		if o == n {
			continue
		}
		out = append(out, fieldDiff{
			Path: fmt.Sprintf("satir %d", i+1),
			Old:  o, New: n,
			Kind: diffKind(o, n),
		})
		if len(out) > diffMaxEntries {
			break
		}
	}
	return out
}
