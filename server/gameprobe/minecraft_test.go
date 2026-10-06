package gameprobe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// fakeConn: yazilan her mesaji bir isleyiciye verir, yanitlari kuyruga koyar.
// chunk > 0 ise yanitlar TCP gibi kucuk parcalara bolunur.
type fakeConn struct {
	onWrite func(b []byte) [][]byte
	queue   [][]byte
	chunk   int
}

func (f *fakeConn) Write(_ context.Context, b []byte) error {
	for _, r := range f.onWrite(b) {
		if f.chunk <= 0 {
			f.queue = append(f.queue, r)
			continue
		}
		for len(r) > 0 {
			n := min(f.chunk, len(r))
			f.queue = append(f.queue, r[:n])
			r = r[n:]
		}
	}
	return nil
}

func (f *fakeConn) Read(ctx context.Context) ([]byte, error) {
	if len(f.queue) == 0 {
		return nil, errors.New("eof")
	}
	b := f.queue[0]
	f.queue = f.queue[1:]
	return b, nil
}

func javaResponse(js string) []byte {
	var body bytes.Buffer
	putVarInt(&body, 0x00)
	putVarInt(&body, int32(len(js)))
	body.WriteString(js)
	return frame(body.Bytes())
}

func TestMinecraftJava(t *testing.T) {
	js := `{"version":{"name":"Paper 1.21.1","protocol":767},
	  "players":{"max":20,"online":3,"sample":[{"name":"§aAlex"},{"name":"Steve"}]},
	  "description":{"text":"§6Zorven ","extra":[{"text":"SMP"}]}}`
	pingSeen := false
	c := &fakeConn{chunk: 7, onWrite: func(b []byte) [][]byte {
		r := &streamReader{buf: b}
		id, _, err := r.readPacket()
		if err != nil {
			t.Fatalf("istemci paketi bozuk: %v", err)
		}
		if id == 0x00 { // handshake + status request ayni yazimda
			if id2, _, err := r.readPacket(); err != nil || id2 != 0x00 {
				t.Fatalf("status istegi yok: %v", err)
			}
			return [][]byte{javaResponse(js)}
		}
		if id == 0x01 {
			pingSeen = true
			return [][]byte{b} // pong = ping'in aynisi
		}
		return nil
	}}
	st := MinecraftJava(context.Background(), c, "mc.example.com", 25565)
	if !st.Online || st.Error != "" {
		t.Fatalf("cevrimici bekleniyordu: %+v", st)
	}
	if st.Version != "Paper 1.21.1" || st.Protocol != 767 || st.PlayersOnline != 3 || st.PlayersMax != 20 {
		t.Fatalf("alanlar yanlis: %+v", st)
	}
	if st.MOTD != "Zorven SMP" {
		t.Fatalf("MOTD bicim kodlari temizlenmeli: %q", st.MOTD)
	}
	if len(st.Players) != 2 || st.Players[0] != "Alex" {
		t.Fatalf("oyuncu listesi yanlis: %v", st.Players)
	}
	if !pingSeen {
		t.Fatal("ping gonderilmedi")
	}
}

func TestMinecraftJavaGarbage(t *testing.T) {
	c := &fakeConn{onWrite: func([]byte) [][]byte { return [][]byte{{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}} }}
	st := MinecraftJava(context.Background(), c, "h", 1)
	if st.Online || st.Error == "" {
		t.Fatalf("bozuk yanit hata vermeli: %+v", st)
	}
	// Asiri buyuk paket boyu reddedilir (bellek sisirme yok).
	var big bytes.Buffer
	putVarInt(&big, maxJavaResponse+1)
	c = &fakeConn{onWrite: func([]byte) [][]byte { return [][]byte{big.Bytes()} }}
	if st := MinecraftJava(context.Background(), c, "h", 1); st.Online {
		t.Fatal("asiri buyuk paket kabul edilmemeli")
	}
}

func TestChatText(t *testing.T) {
	cases := map[string]string{
		`"merhaba"`: "merhaba",
		`{"text":"a","extra":["b",{"text":"c"}]}`: "abc",
		`[{"text":"x"},"y"]`:                      "xy",
	}
	for in, want := range cases {
		if got := chatText([]byte(in)); got != want {
			t.Errorf("%s: %q != %q", in, got, want)
		}
	}
}

func TestMinecraftBedrock(t *testing.T) {
	motd := "MCPE;§bZorven Bedrock;712;1.21.20;5;50;1234567890;Alt;Survival;1;19132;19133;"
	c := &fakeConn{onWrite: func(b []byte) [][]byte {
		if b[0] != 0x01 || !bytes.Equal(b[9:25], raknetMagic) {
			t.Fatalf("ping bicimi yanlis: %x", b)
		}
		var p bytes.Buffer
		p.WriteByte(0x1c)
		p.Write(b[1:9])          // zaman
		p.Write(make([]byte, 8)) // sunucu guid
		p.Write(raknetMagic)
		_ = binary.Write(&p, binary.BigEndian, uint16(len(motd)))
		p.WriteString(motd)
		// Once ilgisiz bir datagram: yok sayilmali.
		return [][]byte{{0x00, 0x01}, p.Bytes()}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	st := MinecraftBedrock(ctx, c)
	if !st.Online || st.Error != "" {
		t.Fatalf("cevrimici bekleniyordu: %+v", st)
	}
	if st.MOTD != "Zorven Bedrock" || st.Protocol != 712 || st.Version != "1.21.20" ||
		st.PlayersOnline != 5 || st.PlayersMax != 50 || st.GameMode != "Survival" {
		t.Fatalf("alanlar yanlis: %+v", st)
	}
}

func TestBedrockTruncated(t *testing.T) {
	c := &fakeConn{onWrite: func(b []byte) [][]byte {
		var p bytes.Buffer
		p.WriteByte(0x1c)
		p.Write(make([]byte, 16))
		p.Write(raknetMagic)
		_ = binary.Write(&p, binary.BigEndian, uint16(500)) // olandan uzun
		p.WriteString("MCPE;x")
		return [][]byte{p.Bytes()}
	}}
	if st := MinecraftBedrock(context.Background(), c); st.Online || st.Error == "" {
		t.Fatalf("kesik yanit hata vermeli: %+v", st)
	}
}
