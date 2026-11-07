package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Eski sunucunun uretttigi http_request JSON'u yeni alanlar olmadan da cozulur.
func TestHTTPRequestOldJSONDecodes(t *testing.T) {
	old := `{"type":"http_request","req_id":7,"tunnel_id":"t","method":"POST","path":"/x","query":"","headers":{},"has_body":true,"remote_addr":"1.2.3.4"}`
	var r HTTPRequest
	if err := json.Unmarshal([]byte(old), &r); err != nil {
		t.Fatal(err)
	}
	if r.HTTPMajor != 0 || r.TETrailers {
		t.Fatalf("eski JSON'da yeni alanlar sifir olmali: %+v", r)
	}
}

// Yeni alanlar bos iken JSON'a yazilmaz (eski ajanlara hicbir sey eklenmez).
func TestHTTPRequestNewFieldsOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(HTTPRequest{Type: TypeHTTPRequest, ReqID: 1})
	if strings.Contains(string(b), "http_major") || strings.Contains(string(b), "te_trailers") {
		t.Fatalf("bos yeni alanlar yazilmamali: %s", b)
	}
	b, _ = json.Marshal(HTTPRequest{Type: TypeHTTPRequest, HTTPMajor: 2, TETrailers: true})
	if !strings.Contains(string(b), `"http_major":2`) || !strings.Contains(string(b), `"te_trailers":true`) {
		t.Fatalf("dolu alanlar yazilmali: %s", b)
	}
}

// Eski bir alici (yalniz bilinen alanlari okuyan) yeni mesajda hata vermez ve
// trailers mesaji kendi basina gidip gelir.
func TestHTTPTrailersRoundTrip(t *testing.T) {
	in := HTTPTrailers{Type: TypeHTTPTrailers, ReqID: 9, Trailers: map[string][]string{"Grpc-Status": {"0"}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil || env.Type != TypeHTTPTrailers {
		t.Fatalf("zarf: %v %q", err, env.Type)
	}
	var out HTTPTrailers
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.ReqID != 9 || out.Trailers["Grpc-Status"][0] != "0" {
		t.Fatalf("gidis-donus bozuk: %+v", out)
	}
	if FeatureHTTPTrailers != "http_trailers" {
		t.Fatal("yetenek adi degismemeli (protokol sabiti)")
	}
}
