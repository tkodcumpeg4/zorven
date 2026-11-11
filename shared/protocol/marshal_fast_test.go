package protocol

import (
	"encoding/json"
	"testing"
)

// A1 #6: Marshal'in hizli yolu (on ek denetimi) tam denetimle (PeekType) ayni
// karari vermeli.
func TestMarshalFastPathSameAsFullCheck(t *testing.T) {
	type typeNotFirst struct {
		ReqID uint64      `json:"req_id"`
		Type  MessageType `json:"type"`
	}
	type typeLess struct {
		ReqID uint64 `json:"req_id"`
	}
	cases := []struct {
		name string
		v    any
		t    MessageType
		ok   bool
	}{
		{"window_update", WindowUpdate{Type: TypeWindowUpdate, ReqID: 1, Increment: 5}, TypeWindowUpdate, true},
		{"yalniz type", Ping{Type: TypePing}, TypePing, true},
		{"yanlis tip", WindowUpdate{Type: TypeWindowUpdate}, TypeCancel, false},
		{"bos tip", WindowUpdate{}, TypeWindowUpdate, false},
		{"type ilk degil", typeNotFirst{ReqID: 3, Type: TypeCancel}, TypeCancel, true},
		{"type ilk degil yanlis", typeNotFirst{ReqID: 3, Type: TypeCancel}, TypePing, false},
		{"type alani yok", typeLess{ReqID: 1}, TypeCancel, false},
		{"harita", map[string]any{"type": "cancel", "req_id": 1}, TypeCancel, true},
		{"harita yanlis", map[string]any{"type": "cancel"}, TypePing, false},
		{"on ek eslesir ama tip uzun", Cancel{Type: "cancel_x"}, TypeCancel, false},
		{"kacisli tip", Cancel{Type: "a<b"}, "a<b", true},
		{"http_request caps", HTTPRequest{Type: TypeHTTPRequest, ReqID: 9, Caps: []string{ReqCapBodyCredit}}, TypeHTTPRequest, true},
	}
	for _, c := range cases {
		data, err := Marshal(c.v, c.t)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v, ok bekleniyordu=%v", c.name, err, c.ok)
		}
		if err != nil {
			continue
		}
		if got, perr := PeekType(data); perr != nil || got != c.t {
			t.Fatalf("%s: PeekType=%q,%v", c.name, got, perr)
		}
	}
}

// typePrefixMatches true diyorsa PeekType ayni tipi vermeli (hizli yol hicbir
// zaman tam denetimin reddedecegi bir ciktiyi kabul etmemeli).
func TestTypePrefixImpliesPeek(t *testing.T) {
	inputs := []string{
		`{"type":"ping"}`, `{"type":"ping","a":1}`, `{"type":"ping" }`, `{"type":"pingx"}`,
		`{"type":"ping"`, `{"type":"ping",`, `{"type":"ping","type":"pong"}`,
		`{"type":"","a":1}`, `{"typ":"ping"}`,
	}
	for _, in := range inputs {
		if !typePrefixMatches([]byte(in), TypePing) {
			continue
		}
		// json.Marshal ciktisi hicbir zaman yinelenen anahtar ya da yarim JSON
		// uretmez; yine de on ekin tek basina guvenli oldugu kosullari belgeler.
		var env Envelope
		if err := json.Unmarshal([]byte(in), &env); err != nil {
			continue // bozuk JSON: Marshal ciktisi olamaz
		}
		if in == `{"type":"ping","type":"pong"}` {
			continue // yinelenen anahtar: json.Marshal uretemez
		}
		if env.Type != TypePing {
			t.Fatalf("%s: on ek eslesti ama tip %q", in, env.Type)
		}
	}
}

func BenchmarkMarshalWindowUpdate(b *testing.B) {
	m := WindowUpdate{Type: TypeWindowUpdate, ReqID: 123456, Increment: 1 << 20}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Marshal(m, TypeWindowUpdate); err != nil {
			b.Fatal(err)
		}
	}
}
