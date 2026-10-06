// Package gameprobe, tunel arkasindaki oyun sunucularinin durumunu sorgular
// (FAZ 4 / F24). Sorgu tunelin ajan oturumu uzerinden gider; sunucunun
// internete acik olmasi veya ziyaretci trafigine karismasi gerekmez.
//
// Desteklenen: Minecraft Java (TCP, Server List Ping) ve Minecraft Bedrock
// (UDP, RakNet unconnected ping).
package gameprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Conn, bir tunel akisinin mesaj-tabanli gorunumu. TCP'de Read parca parca
// bayt dondurebilir; UDP'de her Read bir datagramdir.
type Conn interface {
	Write(ctx context.Context, b []byte) error
	Read(ctx context.Context) ([]byte, error)
}

// Status, bir oyun sunucusunun anlik durumu.
type Status struct {
	Kind          string   `json:"kind"`
	Online        bool     `json:"online"`
	Version       string   `json:"version,omitempty"`
	Protocol      int      `json:"protocol,omitempty"`
	PlayersOnline int      `json:"players_online"`
	PlayersMax    int      `json:"players_max"`
	Players       []string `json:"players,omitempty"` // Java ornek listesi (sunucu verirse)
	MOTD          string   `json:"motd,omitempty"`
	GameMode      string   `json:"game_mode,omitempty"` // Bedrock
	LatencyMS     float64  `json:"latency_ms"`
	Error         string   `json:"error,omitempty"`
}

const (
	KindJava    = "minecraft_java"
	KindBedrock = "minecraft_bedrock"

	maxJavaResponse = 256 * 1024 // durum JSON'u icin ust sinir (favicon dahil)
	maxSamplePlayer = 12
)

// --- Java (Server List Ping) --------------------------------------------------

func putVarInt(buf *bytes.Buffer, v int32) {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			buf.WriteByte(byte(u))
			return
		}
		buf.WriteByte(byte(u&0x7F | 0x80))
		u >>= 7
	}
}

func frame(payload []byte) []byte {
	var b bytes.Buffer
	putVarInt(&b, int32(len(payload)))
	b.Write(payload)
	return b.Bytes()
}

// streamReader, TCP parcalarini bayt akisina cevirir.
type streamReader struct {
	ctx  context.Context
	conn Conn
	buf  []byte
}

func (r *streamReader) fill(n int) error {
	for len(r.buf) < n {
		if r.conn == nil {
			return errors.New("paket beklenenden kisa")
		}
		chunk, err := r.conn.Read(r.ctx)
		if err != nil {
			return err
		}
		r.buf = append(r.buf, chunk...)
	}
	return nil
}

func (r *streamReader) byte() (byte, error) {
	if err := r.fill(1); err != nil {
		return 0, err
	}
	b := r.buf[0]
	r.buf = r.buf[1:]
	return b, nil
}

func (r *streamReader) varInt() (int, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int(int32(v)), nil
		}
	}
	return 0, errors.New("varint cok uzun")
}

func (r *streamReader) bytes(n int) ([]byte, error) {
	if err := r.fill(n); err != nil {
		return nil, err
	}
	out := r.buf[:n:n]
	r.buf = r.buf[n:]
	return out, nil
}

// readPacket, bir Minecraft paketini (id + govde) okur.
func (r *streamReader) readPacket() (int, *streamReader, error) {
	n, err := r.varInt()
	if err != nil {
		return 0, nil, err
	}
	if n <= 0 || n > maxJavaResponse {
		return 0, nil, fmt.Errorf("gecersiz paket boyu %d", n)
	}
	body, err := r.bytes(n)
	if err != nil {
		return 0, nil, err
	}
	pr := &streamReader{buf: body}
	id, err := pr.varInt()
	if err != nil {
		return 0, nil, err
	}
	return id, pr, nil
}

type javaStatus struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
		Sample []struct {
			Name string `json:"name"`
		} `json:"sample"`
	} `json:"players"`
	Description json.RawMessage `json:"description"`
}

// MinecraftJava, Server List Ping ile durumu sorgular. host/port el sikismada
// sunucuya bildirilen adrestir (sanal host kullanan proxy'ler icin onemli).
func MinecraftJava(ctx context.Context, c Conn, host string, port uint16) Status {
	st := Status{Kind: KindJava}

	var hs bytes.Buffer
	putVarInt(&hs, 0x00)
	putVarInt(&hs, -1) // protokol: durum sorgusunda "herhangi"
	putVarInt(&hs, int32(len(host)))
	hs.WriteString(host)
	_ = binary.Write(&hs, binary.BigEndian, port)
	putVarInt(&hs, 1) // sonraki durum: status
	msg := append(frame(hs.Bytes()), frame([]byte{0x00})...)
	start := time.Now()
	if err := c.Write(ctx, msg); err != nil {
		st.Error = "sunucuya yazilamadi"
		return st
	}

	r := &streamReader{ctx: ctx, conn: c}
	id, pr, err := r.readPacket()
	if err != nil || id != 0x00 {
		st.Error = "gecerli durum yaniti alinamadi"
		return st
	}
	st.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	slen, err := pr.varInt()
	if err != nil || slen < 0 || slen > len(pr.buf) {
		st.Error = "durum yaniti bozuk"
		return st
	}
	raw, _ := pr.bytes(slen)
	var js javaStatus
	if err := json.Unmarshal(raw, &js); err != nil {
		st.Error = "durum JSON'u cozulemedi"
		return st
	}
	st.Online = true
	st.Version = stripFormatting(js.Version.Name)
	st.Protocol = js.Version.Protocol
	st.PlayersOnline = js.Players.Online
	st.PlayersMax = js.Players.Max
	for _, p := range js.Players.Sample {
		if len(st.Players) >= maxSamplePlayer {
			break
		}
		if name := stripFormatting(p.Name); name != "" {
			st.Players = append(st.Players, name)
		}
	}
	st.MOTD = stripFormatting(chatText(js.Description))

	// Daha dogru gecikme: ping/pong (sunucu desteklemezse ilk olcum kalir).
	var ping bytes.Buffer
	putVarInt(&ping, 0x01)
	_ = binary.Write(&ping, binary.BigEndian, time.Now().UnixNano())
	start = time.Now()
	if err := c.Write(ctx, frame(ping.Bytes())); err == nil {
		if id, _, err := r.readPacket(); err == nil && id == 0x01 {
			st.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
		}
	}
	return st
}

// chatText, Minecraft sohbet bileseninden (dize, nesne veya dizi) duz metin cikarir.
func chatText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var b strings.Builder
		for _, a := range arr {
			b.WriteString(chatText(a))
		}
		return b.String()
	}
	var obj struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		var b strings.Builder
		b.WriteString(obj.Text)
		for _, e := range obj.Extra {
			b.WriteString(chatText(e))
		}
		return b.String()
	}
	return ""
}

// stripFormatting, "§x" renk/bicim kodlarini ve bas/son boslugu atar.
func stripFormatting(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '§' {
			i++ // kodu da atla
			continue
		}
		b.WriteRune(rs[i])
	}
	return strings.TrimSpace(b.String())
}

// --- Bedrock (RakNet unconnected ping) -----------------------------------------

var raknetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// MinecraftBedrock, RakNet unconnected ping ile durumu sorgular.
func MinecraftBedrock(ctx context.Context, c Conn) Status {
	st := Status{Kind: KindBedrock}

	var ping bytes.Buffer
	ping.WriteByte(0x01)
	_ = binary.Write(&ping, binary.BigEndian, time.Now().UnixMilli())
	ping.Write(raknetMagic)
	guid := make([]byte, 8)
	_, _ = rand.Read(guid)
	ping.Write(guid)

	start := time.Now()
	if err := c.Write(ctx, ping.Bytes()); err != nil {
		st.Error = "sunucuya yazilamadi"
		return st
	}
	for {
		pkt, err := c.Read(ctx)
		if err != nil {
			st.Error = "yanit alinamadi"
			return st
		}
		// 0x1c | zaman(8) | sunucu guid(8) | magic(16) | uzunluk(2) | dize
		if len(pkt) < 35 || pkt[0] != 0x1c || !bytes.Equal(pkt[17:33], raknetMagic) {
			continue // ilgisiz datagram; baglam suresi dolana kadar bekle
		}
		st.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
		n := int(binary.BigEndian.Uint16(pkt[33:35]))
		if 35+n > len(pkt) {
			st.Error = "yanit bozuk"
			return st
		}
		parseBedrockMOTD(&st, string(pkt[35:35+n]))
		return st
	}
}

// parseBedrockMOTD: "MCPE;motd;protokol;surum;cevrimici;azami;sunucuID;alt-motd;oyun modu;..."
func parseBedrockMOTD(st *Status, s string) {
	f := strings.Split(s, ";")
	if len(f) < 6 {
		st.Error = "yanit bozuk"
		return
	}
	st.Online = true
	st.MOTD = stripFormatting(f[1])
	st.Protocol, _ = strconv.Atoi(f[2])
	st.Version = f[3]
	st.PlayersOnline, _ = strconv.Atoi(f[4])
	st.PlayersMax, _ = strconv.Atoi(f[5])
	if len(f) > 8 {
		st.GameMode = f[8]
	}
}
