package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A4 #10: `null` govdesi reddedilir (eskiden sifir degerli govdeyle ayari sifirliyordu);
// `{}`, bilinmeyen alan ve bozuk JSON davranisi degismez.
func TestDecodeNullReddedilir(t *testing.T) {
	type body struct {
		Mode string `json:"mode"`
	}
	run := func(in string) (int, body, bool) {
		var b body
		b.Mode = "onceki"
		rec := httptest.NewRecorder()
		ok := decode(rec, httptest.NewRequest("PUT", "/x", strings.NewReader(in)), &b)
		code := rec.Code
		if ok {
			code = http.StatusOK
		}
		return code, b, ok
	}
	for _, in := range []string{"null", " null\n", "", "{", `{"bilinmeyen":1}`, "[1]"} {
		if code, _, ok := run(in); ok || code != http.StatusBadRequest {
			t.Fatalf("%q reddedilmeliydi: %d", in, code)
		}
	}
	if _, b, ok := run(`{}`); !ok || b.Mode != "onceki" {
		t.Fatalf("{} kabul edilmeli ve alan degismemeli: %v %+v", ok, b)
	}
	if _, b, ok := run(`{"mode":"basic"}`); !ok || b.Mode != "basic" {
		t.Fatalf("normal govde: %v %+v", ok, b)
	}
}
