package agent

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Girdi durum takibi ve kuyruk politikasi (platformdan bagimsiz, test edilebilir).
//
// NEDEN: uzak makinede basili tus/dugme kalirsa (oturum kapandi, baglanti
// koptu, keyup kuyruktan dustu) kullanici kendi makinesinde "takili tus"
// yasar. Bu dosya (1) hangi tus/dugmenin BASILI oldugunu izler ve oturum
// bitince hepsini birakir, (2) kuyruk tasarken YALNIZ mousemove'u dusurur.

// inputTracker, bir oturumun basili tus ve fare dugmelerini izler.
// Yalniz applyLoop goroutine'i kullanir (kilitsiz).
type inputTracker struct {
	keys    map[string]protocol.ScreenInput
	order   []string // basilma sirasi (birakma tersten)
	buttons map[int]protocol.ScreenInput
	// konum: son bilinen fare konumu (mouseup'a tasinir).
	lastX, lastY float64
}

func keyID(ev protocol.ScreenInput) string {
	if ev.Code != "" {
		return "c:" + ev.Code
	}
	if ev.Key != "" {
		return "k:" + ev.Key
	}
	return ""
}

// Observe, UYGULANAN bir olayi isler.
func (t *inputTracker) Observe(ev protocol.ScreenInput) {
	switch ev.Kind {
	case "keydown":
		id := keyID(ev)
		if id == "" {
			return
		}
		if t.keys == nil {
			t.keys = make(map[string]protocol.ScreenInput)
		}
		if _, ok := t.keys[id]; !ok {
			t.order = append(t.order, id)
		}
		t.keys[id] = ev
	case "keyup":
		id := keyID(ev)
		if _, ok := t.keys[id]; !ok {
			return
		}
		delete(t.keys, id)
		for i, o := range t.order {
			if o == id {
				t.order = append(t.order[:i], t.order[i+1:]...)
				break
			}
		}
	case "mousedown":
		if t.buttons == nil {
			t.buttons = make(map[int]protocol.ScreenInput)
		}
		t.buttons[ev.Button] = ev
		t.lastX, t.lastY = ev.X, ev.Y
	case "mouseup":
		delete(t.buttons, ev.Button)
		t.lastX, t.lastY = ev.X, ev.Y
	case "mousemove":
		t.lastX, t.lastY = ev.X, ev.Y
	}
}

// Held, basili tus + dugme sayisi.
func (t *inputTracker) Held() int { return len(t.keys) + len(t.buttons) }

// ReleaseAll, basili her sey icin sentetik birakma olaylari doner ve izlemeyi
// sifirlar. Sira: once fare dugmeleri, sonra tuslar BASILMA SIRASININ TERSI
// (modifier'lar en son birakilir).
func (t *inputTracker) ReleaseAll() []protocol.ScreenInput {
	var out []protocol.ScreenInput
	for b := range t.buttons {
		out = append(out, protocol.ScreenInput{Kind: "mouseup", X: t.lastX, Y: t.lastY, Button: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Button < out[j].Button })
	for i := len(t.order) - 1; i >= 0; i-- {
		ev := t.keys[t.order[i]]
		out = append(out, protocol.ScreenInput{Kind: "keyup", Key: ev.Key, Code: ev.Code})
	}
	t.keys, t.order, t.buttons = nil, nil, nil
	return out
}

// ---------------------------------------------------------------------------
// Girdi kuyrugu

const (
	// screenInputQueue, oturum basina yumusak sinir (mousemove bunu asamaz).
	screenInputQueue = 64
	// screenInputHardCap, kritik olaylar icin mutlak sinir (bellek korumasi).
	screenInputHardCap = 4096
)

// inputQueue, kuyruk politikasi: yalniz mousemove dusurulebilir.
//
//   - Ardisik mousemove'lar BIRLESIR (son konum kazanir).
//   - Yumusak sinir dolunca yeni mousemove dusurulur; kritik olay (tus/dugme/
//     tekerlek/metin) gelirse EN ESKI bekleyen mousemove atilip yer acilir.
//     Bekleyen mousemove yoksa kuyruk hardCap'e kadar buyur.
type inputQueue struct {
	mu      sync.Mutex
	q       []protocol.ScreenInput
	softCap int
	hardCap int
	sig     chan struct{}
}

func newInputQueue(soft, hard int) *inputQueue {
	return &inputQueue{softCap: soft, hardCap: hard, sig: make(chan struct{}, 1)}
}

// Push, olayi kuyruga koyar; olay kabul edildiyse true doner.
func (q *inputQueue) Push(ev protocol.ScreenInput) bool {
	q.mu.Lock()
	ok := q.pushLocked(ev)
	q.mu.Unlock()
	if ok {
		select {
		case q.sig <- struct{}{}:
		default:
		}
	}
	return ok
}

func (q *inputQueue) pushLocked(ev protocol.ScreenInput) bool {
	isMove := ev.Kind == "mousemove"
	if isMove && len(q.q) > 0 && q.q[len(q.q)-1].Kind == "mousemove" {
		q.q[len(q.q)-1] = ev // birlestir
		return true
	}
	if len(q.q) < q.softCap {
		q.q = append(q.q, ev)
		return true
	}
	if isMove {
		return false // dolu: mousemove dusurulur
	}
	// Kritik olay: en eski mousemove'u at.
	for i, e := range q.q {
		if e.Kind == "mousemove" {
			q.q = append(q.q[:i], q.q[i+1:]...)
			q.q = append(q.q, ev)
			return true
		}
	}
	if len(q.q) < q.hardCap {
		q.q = append(q.q, ev)
		return true
	}
	return false
}

// Pop, siradaki olayi doner (bloklamaz).
func (q *inputQueue) Pop() (protocol.ScreenInput, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.q) == 0 {
		return protocol.ScreenInput{}, false
	}
	ev := q.q[0]
	q.q = q.q[1:]
	if len(q.q) == 0 {
		q.q = nil
	}
	return ev, true
}

// Len, bekleyen olay sayisi.
func (q *inputQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.q)
}

// ---------------------------------------------------------------------------
// Modifier izleyici (enjekte edilen modifier durumu)

// Modifier siniflari.
const (
	modClassShift = iota
	modClassCtrl
	modClassAlt
	modClassWin
	modClassCount
)

// heldMods, uzak makineye ENJEKTE ETTIGIMIZ modifier tuslarini izler: gercek
// modifier down/up olaylari oldugu gibi iletilir; kisayol sarmalamasi yalniz
// modifier zaten basili DEGILSE sentetik basma/birakma yapar (aksi halde
// kullanicinin tuttugu Ctrl, kisayol bitince birakilmis olurdu).
type heldMods struct {
	mu sync.Mutex
	n  [modClassCount]map[uint16]struct{}
}

func (h *heldMods) Down(class int, id uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n[class] == nil {
		h.n[class] = make(map[uint16]struct{})
	}
	h.n[class][id] = struct{}{}
}

func (h *heldMods) Up(class int, id uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.n[class], id)
}

func (h *heldMods) Held(class int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.n[class]) > 0
}

// HeldExact, tam bu tus (or. sol Ctrl) basili mi.
func (h *heldMods) HeldExact(class int, id uint16) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.n[class][id]
	return ok
}

// Reset, tum izlemeyi sifirlar (masaustu degisimi vb.).
func (h *heldMods) Reset() {
	h.mu.Lock()
	h.n = [modClassCount]map[uint16]struct{}{}
	h.mu.Unlock()
}

// ---------------------------------------------------------------------------
// AltGr ve metin yardimcilari

// isAltGrChar, Ctrl+Alt basili ve tusun URETTIGI karakter fiziksel tusun duz
// karakterinden farkliysa (or. Almanca AltGr+Q = "@") true doner. Tarayicilar
// AltGr'yi Ctrl+Alt olarak bildirir; bu durumda karakter Unicode yoluyla
// yazilmalidir. Gercek kisayol (Ctrl+Alt+T: key "t", code KeyT) false doner.
func isAltGrChar(msg protocol.ScreenInput) bool {
	if !msg.Ctrl || !msg.Alt || msg.Meta {
		return false
	}
	r := oneRune(msg.Key)
	if r == 0 || r == ' ' || unicode.IsControl(r) || !unicode.IsPrint(r) {
		return false
	}
	var plain string
	switch {
	case strings.HasPrefix(msg.Code, "Key") && len(msg.Code) == 4:
		plain = strings.ToLower(msg.Code[3:])
	case strings.HasPrefix(msg.Code, "Digit") && len(msg.Code) == 6:
		plain = msg.Code[5:]
	}
	if plain != "" {
		return !strings.EqualFold(string(r), plain)
	}
	if msg.Code == "" {
		return false
	}
	// Harf/rakam kodu olmayan tuslar: yalniz tipik AltGr ciktilari.
	return r > 0x7F || strings.ContainsRune("@{}[]\\|~", r)
}

// oneRune, key tek bir karakter ise onu doner (yoksa 0). Platform
// dosyalarindaki singleRune/oneRune ile ayni anlam; tag'siz burada.
func oneRune(key string) rune {
	rs := []rune(key)
	if len(rs) == 1 {
		return rs[0]
	}
	return 0
}

// textUTF16, metni SendInput KEYEVENTF_UNICODE icin UTF-16 birimlerine cevirir
// (BMP disi karakterler vekil cifti olur). Gecersiz UTF-8 U+FFFD olur.
// maxBytes'tan uzun metin kesilir (rune sinirinda).
func textUTF16(s string, maxBytes int) []uint16 {
	if maxBytes > 0 && len(s) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return utf16.Encode([]rune(s))
}

// ---------------------------------------------------------------------------
// Masaustu yeniden baglanma kisici

// desktopGuard, girdi masaustune yeniden baglanmayi kisar.
type desktopGuard struct {
	last  time.Time
	every time.Duration
}

// Due, yeniden baglanma zamani geldiyse true doner ve sayaci yeniler.
func (g *desktopGuard) Due(now time.Time) bool {
	if g.every <= 0 {
		g.every = 2 * time.Second
	}
	if g.last.IsZero() || now.Sub(g.last) >= g.every {
		g.last = now
		return true
	}
	return false
}
