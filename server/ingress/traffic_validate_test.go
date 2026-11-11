package ingress

import "testing"

// A4 #12: trafik politikasi ve policy set_header/redirect kayit dogrulamasi.
func TestValidateTrafficConfig(t *testing.T) {
	ok := []string{
		``, `null`, `{}`,
		`{"request_headers":{"set":{"X-Foo":"bar"},"remove":["X-Bar"]}}`,
		`{"response_headers":{"set":{"X-Frame-Options":"DENY"},"remove":["Server"]}}`,
		`{"redirects":[{"match_prefix":"/eski","location":"/yeni","status":301}]}`,
		`{"redirects":[{"match_prefix":"/a","location":"https://x.test/b"}]}`, // status 0 -> 302
		`{"redirects":[{"match_prefix":"/a","location":"","status":0}]}`,      // bos satir: yok sayilir
	}
	for _, c := range ok {
		if err := ValidateTrafficConfig([]byte(c)); err != nil {
			t.Errorf("gecerli config reddedildi %s: %v", c, err)
		}
	}
	bad := []string{
		`[]`,
		`{"request_headers":{"set":{"":"x"}}}`,
		`{"request_headers":{"set":{"X A":"x"}}}`,
		`{"request_headers":{"set":{"X-A\r\nX-B":"x"}}}`,
		`{"request_headers":{"set":{"X-A":"x\r\nSet-Cookie: a=b"}}}`,
		`{"response_headers":{"remove":[""]}}`,
		`{"redirects":[{"location":"/x\r\nSet-Cookie: a=b"}]}`,
		`{"redirects":[{"location":"/x","status":999}]}`,
		`{"redirects":[{"location":"/x","status":-1}]}`,
		`{"redirects":[{"location":"/x","status":303}]}`,
	}
	for _, c := range bad {
		if err := ValidateTrafficConfig([]byte(c)); err == nil {
			t.Errorf("gecersiz config kabul edildi: %s", c)
		}
	}
}

func TestValidatePolicyConfigHeadersAndRedirect(t *testing.T) {
	bad := []string{
		`{"rules":[{"action":{"type":"set_header","request":{"set":{"":"x"}}}}]}`,
		`{"rules":[{"action":{"type":"set_header","response":{"set":{"X-A":"a\nb"}}}}]}`,
		`{"rules":[{"action":{"type":"set_header","request":{"remove":["bad name"]}}}]}`,
		`{"rules":[{"action":{"type":"redirect","location":"/x\r\nX: y"}}]}`,
		`{"rules":[{"action":{"type":"redirect","location":"/x","status":399}}]}`,
	}
	for _, c := range bad {
		if err := ValidatePolicyConfig([]byte(c)); err == nil {
			t.Errorf("gecersiz policy kabul edildi: %s", c)
		}
	}
	ok := `{"rules":[{"action":{"type":"set_header","request":{"set":{"X-Ok":"1"},"remove":["X-Old"]}}}]}`
	if err := ValidatePolicyConfig([]byte(ok)); err != nil {
		t.Errorf("gecerli set_header reddedildi: %v", err)
	}
}
