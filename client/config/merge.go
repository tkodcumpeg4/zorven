package config

// Ortak ayar dosyasi yazimi. CLI (client) ve masaustu uygulamasi AYNI config.json
// dosyasini (kullanici ayar dizini/zorven/config.json) kullanir ama her biri kendi
// Go yapisini bilir: CLI enable_terminal/enable_screen/..., masaustu auto_connect/
// no_terminal/hide_to_tray/token_enc/... Yapiyi dogrudan json.Marshal ile yazan bir
// yazici, digerinin alanlarini (ve gelecekte eklenecek her alani) siler; ornegin
// masaustu token_enc'i silinince oturum duser.
//
// MarshalMerged, dosyayi okuyup YALNIZ yazicinin yapisinin bildigi (json etiketli)
// anahtarlari yapinin degerleriyle gunceller; bilinmeyen alanlar oldugu gibi
// (sira ve ham deger, buyuk sayilar dahil) korunur. Yapinin bildigi ama su an
// bos oldugu icin yazilmayan (omitempty) anahtar dosyadan SILINIR: aksi halde bir
// ayar (or. enable_deploy) kapatilamazdi.
//
// Atomik yazma, dosya izinleri ve Windows ACL'i yaziciya aittir (SaveFile, masaustu
// saveConfig): bu paket yalniz yazilacak icerigi uretir.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"
)

// maxConfigBytes, ayar dosyasi icin makul ust sinir; daha buyuk dosya okunmaz
// (bozuk sayilir ve korunacak alan aranmaz).
const maxConfigBytes = 4 << 20

// MergeResult, MergeJSON/MarshalMerged'in sonucu.
type MergeResult struct {
	// Data, diske yazilacak (iki bosluk girintili) JSON.
	Data []byte
	// Preserved, mevcut dosyadan korunan ve yazicinin bilmedigi anahtarlar.
	Preserved []string
	// Corrupt, dosya vardi ama bos degildi ve gecerli bir JSON nesnesi degildi
	// (ya da cok buyuktu): korunacak bir sey bulunamadi, Data yalniz yapiyi icerir.
	// Yazici dosyayi ezmeden once yedeklemelidir.
	Corrupt bool
}

type rawField struct {
	key string
	val json.RawMessage
}

// parseObject, JSON nesnesini sirali (anahtar, ham deger) listesine ayirir.
// Nesne degilse ya da sonrasinda baska veri varsa hata doner (json.Unmarshal'in
// "gecersiz" saydigi dosyalar burada da gecersizdir).
func parseObject(b []byte) ([]rawField, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("json nesnesi degil")
	}
	var out []rawField
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, errors.New("beklenmeyen anahtar")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, rawField{key: k, val: raw})
	}
	if _, err := dec.Token(); err != nil { // kapanis }
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("nesneden sonra veri var")
	}
	return out, nil
}

// jsonKeys, v'nin (yapi ya da yapi isaretcisi) json anahtar adlarini alan sirasiyla
// verir: omitempty olanlar dahil, "-" ile gizlenenler haric. Etiketsiz gomulu yapilar
// encoding/json gibi duzlestirilir.
func jsonKeys(v any) []string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return structKeys(t)
}

func structKeys(t reflect.Type) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				keys = append(keys, structKeys(ft)...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	return keys
}

// canonicalKey, mevcut dosyadaki bir anahtarin yazicinin bildigi hangi anahtara
// denk geldigini soyler (once birebir, sonra encoding/json'un yukleme sirasinda
// yaptigi gibi buyuk/kucuk harf duyarsiz); bilinmiyorsa "" doner.
func canonicalKey(k string, known []string) string {
	for _, n := range known {
		if n == k {
			return n
		}
	}
	for _, n := range known {
		if strings.EqualFold(n, k) {
			return n
		}
	}
	return ""
}

// MergeJSON, existing (mevcut dosya icerigi; bos olabilir) ile v'nin JSON'unu
// birlestirir. v bir yapi (ya da yapi isaretcisi) olmalidir.
//
// Sonucun anahtar sirasi: mevcut dosyadaki sira korunur (bilinen anahtarlar yerinde
// guncellenir); dosyada olmayan yeni anahtarlar yapinin alan sirasiyla sona eklenir.
// Mevcut dosya yoksa sonuc json.MarshalIndent(v, "", "  ") ile birebir aynidir.
func MergeJSON(existing []byte, v any) (MergeResult, error) {
	own, err := json.Marshal(v)
	if err != nil {
		return MergeResult{}, err
	}
	ownFields, err := parseObject(own)
	if err != nil {
		return MergeResult{}, errors.New("ayar yapisi json nesnesi olmali: " + err.Error())
	}
	ownVal := make(map[string]json.RawMessage, len(ownFields))
	for _, f := range ownFields {
		ownVal[f.key] = f.val
	}
	known := jsonKeys(v)

	var res MergeResult
	var old []rawField
	if len(bytes.TrimSpace(existing)) > 0 {
		o, perr := parseObject(existing)
		if perr != nil {
			res.Corrupt = true
		} else {
			old = o
		}
	}

	out := make([]rawField, 0, len(old)+len(ownFields))
	placed := map[string]bool{}
	for _, f := range old {
		kn := canonicalKey(f.key, known)
		if kn == "" {
			out = append(out, f) // bilinmeyen alan (digerinin ya da gelecegin): oldugu gibi
			res.Preserved = append(res.Preserved, f.key)
			continue
		}
		// Bilinen anahtar: yerinde guncelle. Yapi onu yazmiyorsa (omitempty bos) ya da
		// tekrar ediyorsa dosyadan dusur.
		if nv, ok := ownVal[kn]; ok && !placed[kn] {
			out = append(out, rawField{key: kn, val: nv})
			placed[kn] = true
		}
	}
	for _, f := range ownFields {
		if !placed[f.key] {
			out = append(out, f)
		}
	}

	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, f := range out {
		if i > 0 {
			compact.WriteByte(',')
		}
		kb, _ := json.Marshal(f.key)
		compact.Write(kb)
		compact.WriteByte(':')
		compact.Write(f.val)
	}
	compact.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		return MergeResult{}, err
	}
	res.Data = indented.Bytes()
	return res, nil
}

// backupBeforeOverwrite, okunamayan (bozuk) ayar dosyasini ezilmeden once yan yana
// saklar: <dosya>.bak; ayni icerik zaten oradaysa (Load yedekledi) tekrar yazilmaz,
// .bak baska icerikliyse eski yedek ezilmeden zaman damgali bir yedek acilir.
// Yedek token icerebilir: 0600 (Windows'ta korumali DACL). En iyi gayret: hata yoksayilir.
func backupBeforeOverwrite(path string) {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > maxConfigBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	bak := path + ".bak"
	if old, err := os.ReadFile(bak); err == nil {
		if bytes.Equal(old, data) {
			return
		}
		bak = fmt.Sprintf("%s.%s.bak", path, time.Now().Format("20060102-150405"))
	}
	_ = WriteSecretFile(bak, data)
}

// MarshalMerged, path'teki mevcut dosyayi okuyup v ile birlestirir (bkz. MergeJSON).
// Dosya yoksa ya da okunamazsa yalniz v yazilir; bos dosya sorun degildir; bozuk
// (gecersiz JSON, nesne olmayan, cok buyuk) dosyada Corrupt isaretlenir.
func MarshalMerged(path string, v any) (MergeResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return MergeJSON(nil, v)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return MergeJSON(nil, v)
	}
	if len(b) > maxConfigBytes {
		res, merr := MergeJSON(nil, v)
		res.Corrupt = true
		return res, merr
	}
	return MergeJSON(b, v)
}
