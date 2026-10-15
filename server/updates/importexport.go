package updates

// Guncelleme yazilari icin JSON ice/disa aktarma semasi (strict).
//
// Kabul edilen govde bicimleri:
//
//	{ ...tek yazi... }
//	[ {...}, {...} ]
//	{ "posts": [ {...}, {...} ] }
//
// Yazi alanlari: slug, kind (release|update|maintenance; bos = update), version,
// title_tr, body_tr (zorunlu), title_en, body_en, title_ja, body_ja,
// maintenance_start / maintenance_end (RFC3339 veya null), published (bool, vars. false).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Ice aktarma sinirlari.
const (
	MaxImportBytes = 1 << 20 // govdenin toplam boyutu
	MaxImportPosts = 200
)

// ImportPost, ice/disa aktarma semasindaki tek yazi.
type ImportPost struct {
	Slug             string     `json:"slug"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	TitleTR          string     `json:"title_tr"`
	BodyTR           string     `json:"body_tr"`
	TitleEN          string     `json:"title_en"`
	BodyEN           string     `json:"body_en"`
	TitleJA          string     `json:"title_ja"`
	BodyJA           string     `json:"body_ja"`
	MaintenanceStart *time.Time `json:"maintenance_start"`
	MaintenanceEnd   *time.Time `json:"maintenance_end"`
	Published        bool       `json:"published"`
}

// ImportEnvelope, disa aktarma ciktisi.
type ImportEnvelope struct {
	Posts []ImportPost `json:"posts"`
}

// ParsedItem, ayristirilmis tek ogenin sonucu: Post gecerli bicimde cozulduyse
// Err bos, aksi halde Err sebebi (alan dogrulamasi API katmaninda yapilir).
type ParsedItem struct {
	Index int
	Post  ImportPost
	Err   string
}

var importFields = map[string]bool{
	"slug": true, "kind": true, "version": true, "title_tr": true, "body_tr": true,
	"title_en": true, "body_en": true, "title_ja": true, "body_ja": true,
	"maintenance_start": true, "maintenance_end": true, "published": true,
}

// ParseImport, govdeyi strict ayristirir. Bicim/boyut hatasi err olarak doner;
// ogeye ozel hatalar (bilinmeyen alan, tip) ParsedItem.Err icindedir.
func ParseImport(data []byte) ([]ParsedItem, error) {
	if len(data) > MaxImportBytes {
		return nil, fmt.Errorf("govde cok buyuk (en fazla %d bayt)", MaxImportBytes)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("govde bos")
	}
	var raws []json.RawMessage
	switch data[0] {
	case '[':
		if err := json.Unmarshal(data, &raws); err != nil {
			return nil, fmt.Errorf("gecersiz JSON: %v", err)
		}
	case '{':
		var top map[string]json.RawMessage
		if err := json.Unmarshal(data, &top); err != nil {
			return nil, fmt.Errorf("gecersiz JSON: %v", err)
		}
		if pr, ok := top["posts"]; ok {
			if len(top) != 1 {
				return nil, errors.New("\"posts\" ile birlikte baska ust duzey alan olamaz")
			}
			if err := json.Unmarshal(pr, &raws); err != nil {
				return nil, fmt.Errorf("\"posts\" dizi olmali: %v", err)
			}
		} else {
			raws = []json.RawMessage{data}
		}
	default:
		return nil, errors.New("JSON nesne veya dizi olmali")
	}
	if len(raws) == 0 {
		return nil, errors.New("hic yazi yok")
	}
	if len(raws) > MaxImportPosts {
		return nil, fmt.Errorf("en fazla %d yazi ice aktarilabilir", MaxImportPosts)
	}
	out := make([]ParsedItem, len(raws))
	for i, raw := range raws {
		out[i] = ParsedItem{Index: i}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			out[i].Err = "yazi bir JSON nesnesi olmali"
			continue
		}
		var unknown []string
		for k := range keys {
			if !importFields[k] {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			out[i].Err = "bilinmeyen alan(lar): " + strings.Join(unknown, ", ")
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&out[i].Post); err != nil {
			out[i].Err = "gecersiz alan degeri: " + err.Error()
		}
	}
	return out, nil
}

// ExportPost, depo yazisini ice aktarma semasina cevirir.
func ExportPost(p store.UpdatePost) ImportPost {
	return ImportPost{
		Slug: p.Slug, Kind: p.Kind, Version: p.Version,
		TitleTR: p.TitleTR, BodyTR: p.BodyTR, TitleEN: p.TitleEN, BodyEN: p.BodyEN, TitleJA: p.TitleJA, BodyJA: p.BodyJA,
		MaintenanceStart: p.MaintenanceStart, MaintenanceEnd: p.MaintenanceEnd,
		Published: p.PublishedAt != nil,
	}
}
