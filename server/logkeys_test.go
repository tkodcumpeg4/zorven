package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// eskiLogAnahtarlari, docs/operasyon-gozlemlenebilirlik.md §8 sozlugunde
// tercih edilen adin es anlamlilari. Log sorgulari tek ada guvenebilsin diye
// yeni kodda kullanilmamalari bu testle korunur.
var eskiLogAnahtarlari = map[string]string{
	"err": "hata", "error": "hata",
	"tenant_id": "tenant", "kiraci": "tenant", "org": "tenant",
	"client_id": "client", "tunel": "tunnel", "tunnel_id": "tunnel",
	"session_id": "session", "remote_addr": "adres", "addr": "adres", "uzak": "adres",
	"sebep": "neden", "reason": "neden", "host": "fqdn", "hostname": "fqdn",
	"msg": "mesaj", "code": "kod", "user": "kullanici", "user_id": "kullanici",
	"version": "surum", "status": "durum",
}

// logAnahtarlariniTara, dir altindaki (test ve third_party disi) Go dosyalarinda
// log cagrisi gibi gorunen (Debug/Info/Warn/Error/With ve sarmalayicilari)
// cagrilarin anahtar konumundaki eski adlari dondurur. Tip bilgisi kullanilmaz;
// eski adlar yalniz log baglaminda yasak oldugu icin yanlis pozitif dusuktur.
func logAnahtarlariniTara(t *testing.T, dir string) []string {
	t.Helper()
	// cagri adi -> mesajdan sonra anahtarlarin basladigi arguman indeksi
	start := map[string]int{"Debug": 1, "Info": 1, "Warn": 1, "Error": 1, "With": 0,
		"logWarn": 1, "logDebug": 1, "warnFrame": 1, "onLog": 1, "sampledWarn": 3}
	var bulgular []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "third_party" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && p != dir {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || c.Ellipsis.IsValid() {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			s, ok := start[sel.Sel.Name]
			if !ok {
				return true
			}
			for i := s; i < len(c.Args); i += 2 {
				bl, ok := c.Args[i].(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true // anahtar/deger dizisi degil (or. t.Error, Attr)
				}
				k, _ := strconv.Unquote(bl.Value)
				if yeni, eski := eskiLogAnahtarlari[k]; eski {
					bulgular = append(bulgular, fset.Position(bl.Pos()).String()+": \""+k+"\" yerine \""+yeni+"\"")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bulgular
}

func TestLogAlanSozlugu(t *testing.T) {
	for _, b := range logAnahtarlariniTara(t, ".") {
		t.Error(b)
	}
}
