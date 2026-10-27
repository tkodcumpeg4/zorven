package wgnet

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// "disco": dogrudan yol yoklamasi (magicsock'un disco'sunun asgari karsiligi).
// WireGuard anahtarindan AYRI bir Curve25519 cifti ile nacl/box (XSalsa20-
// Poly1305) muhurlenir; boylece yoklamalar yalniz iki dugum arasinda dogrulanir
// ve WireGuard paketleriyle karismaz (WG: ilk bayt 1-4 + 3 sifir; STUN: cookie).
//
// Paket: magic(7) | gonderen disco acik anahtari(32) | nonce(24) | box(plain)
// Plain: tip(1) | zaman damgasi unix sn(8) | txid(12) | ek
//
// Tekrar oynatma korumasi: (a) her paket rastgele 24 baytlik nonce tasir ve
// alici son pencere icindeki nonce'lari tutar, (b) zaman damgasi +-skew
// penceresi disindaysa reddedilir, (c) pong yalniz bizim gonderdigimiz BEKLEYEN
// bir txid'yi yanitlayabilir ve bir kez tuketilir.

var discoMagic = [7]byte{'Z', 'V', 'd', 'i', 's', 'c', 1}

const (
	discoPing byte = 1
	discoPong byte = 2

	discoHdr      = 7 + 32 + 24
	discoPlainMin = 1 + 8 + 12
	// discoMaxPacket: ping/pong'un ustu; bu boyutu asan paket atilir.
	discoMaxPacket = 512

	// Saat farki toleransi ve nonce hatirlama suresi (skew'den uzun olmali).
	discoSkew   = 5 * time.Minute
	replayKeep  = 12 * time.Minute
	replayLimit = 8192
)

// DiscoKey, disco Curve25519 cifti.
type DiscoKey struct {
	Priv [32]byte
	Pub  [32]byte
}

// GenerateDiscoKey, yeni (surece ozel) disco anahtari uretir.
func GenerateDiscoKey() (DiscoKey, error) {
	var k DiscoKey
	if _, err := rand.Read(k.Priv[:]); err != nil {
		return k, err
	}
	k.Priv[0] &= 248
	k.Priv[31] = (k.Priv[31] & 127) | 64
	pub, err := curve25519.X25519(k.Priv[:], curve25519.Basepoint)
	if err != nil {
		return k, err
	}
	copy(k.Pub[:], pub)
	return k, nil
}

// PubB64, sunucuya gonderilen bicim.
func (k DiscoKey) PubB64() string { return base64.StdEncoding.EncodeToString(k.Pub[:]) }

func decodeDiscoPub(b64 string) ([32]byte, bool) {
	var out [32]byte
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

// sharedKey, iki disco anahtarindan on-hesaplanmis nacl/box anahtari.
func (k DiscoKey) sharedKey(peerPub [32]byte) [32]byte {
	var s [32]byte
	box.Precompute(&s, &peerPub, &k.Priv)
	return s
}

// discoMsg: cozulmus yoklama.
type discoMsg struct {
	Kind byte
	TS   time.Time
	Tx   [12]byte
	// Ek: pong'da yoklamanin geldigi kaynak adres (NAT'in gordugu).
	Seen netip.AddrPort
}

// sealDisco, plain'i muhurler.
func sealDisco(self DiscoKey, shared *[32]byte, m discoMsg) ([]byte, error) {
	plain := make([]byte, 0, discoPlainMin+20)
	plain = append(plain, m.Kind)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(m.TS.Unix()))
	plain = append(plain, ts[:]...)
	plain = append(plain, m.Tx[:]...)
	if m.Kind == discoPong && m.Seen.IsValid() {
		sb, _ := m.Seen.MarshalBinary()
		plain = append(plain, sb...)
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	out := make([]byte, 0, discoHdr+len(plain)+box.Overhead)
	out = append(out, discoMagic[:]...)
	out = append(out, self.Pub[:]...)
	out = append(out, nonce[:]...)
	return box.SealAfterPrecomputation(out, plain, &nonce, shared), nil
}

// isDisco, paketin disco onekini tasiyip tasimadigini soyler.
func isDisco(p []byte) bool {
	return len(p) >= discoHdr+box.Overhead+discoPlainMin && len(p) <= discoMaxPacket &&
		[7]byte(p[:7]) == discoMagic
}

// discoSender, paketin iddia ettigi gonderen disco anahtari (dogrulanmamis).
func discoSender(p []byte) (pub [32]byte) {
	copy(pub[:], p[7:39])
	return
}

var errDiscoOpen = errors.New("disco paketi acilamadi")

// openDisco, paketi acar ve alanlari cozer (zaman/tekrar kontrolu cagiranda).
func openDisco(shared *[32]byte, p []byte) (m discoMsg, nonce [24]byte, err error) {
	copy(nonce[:], p[39:63])
	plain, ok := box.OpenAfterPrecomputation(nil, p[discoHdr:], &nonce, shared)
	if !ok || len(plain) < discoPlainMin {
		return m, nonce, errDiscoOpen
	}
	m.Kind = plain[0]
	if m.Kind != discoPing && m.Kind != discoPong {
		return m, nonce, errDiscoOpen
	}
	m.TS = time.Unix(int64(binary.BigEndian.Uint64(plain[1:9])), 0)
	copy(m.Tx[:], plain[9:21])
	if len(plain) > 21 {
		var ap netip.AddrPort
		if ap.UnmarshalBinary(plain[21:]) == nil {
			m.Seen = ap
		}
	}
	return m, nonce, nil
}

// replayCache, son pencerede gorulen nonce'lari tutar.
type replayCache struct {
	mu   sync.Mutex
	seen map[[24]byte]time.Time
	gc   time.Time
}

func newReplayCache() *replayCache { return &replayCache{seen: map[[24]byte]time.Time{}} }

// check, nonce daha once goruldu ise false doner; degilse kaydeder.
func (r *replayCache) check(n [24]byte, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.gc) > time.Minute || len(r.seen) > replayLimit {
		for k, t := range r.seen {
			if now.Sub(t) > replayKeep {
				delete(r.seen, k)
			}
		}
		r.gc = now
		if len(r.seen) > replayLimit { // saldiri: hepsini birak (zaman damgasi yine korur)
			r.seen = map[[24]byte]time.Time{}
		}
	}
	if _, dup := r.seen[n]; dup {
		return false
	}
	r.seen[n] = now
	return true
}

func fillRand(b []byte) { _, _ = rand.Read(b) }
