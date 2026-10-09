package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Kalici terminal oturumlari.
//
// Eskiden kabuk sureci tarayici WS'ine bagliydi: panelde baska sayfaya gecince
// veya sayfa yenilenince kabuk kapaniyordu. Simdi oturum sunucuda yasar; WS
// yalnizca ona "baglanir". Baglanti kopunca oturum termDetachTTL kadar bekler,
// son ciktilar termBufferMax kadar tamponda tutulur ve yeniden baglanan sekmeye
// once tampon, sonra canli akis gonderilir. Istemci (ajan) degisikligi gerekmez.
//
// GUVENLIK: oturum kiraci + istemci + acan kimlige (kullanici / servis hesabi /
// yonetici anahtari) baglidir; ayni kiracidaki baska bir uye ID'yi bilse bile
// baglanamaz.
const (
	termDetachTTL    = 30 * time.Minute
	termBufferMax    = 256 << 10 // sekme basina tekrar oynatilan cikti
	termMaxPerOwner  = 8         // istemci + kimlik basina canli oturum
	termJanitorEvery = time.Minute
)

type termRegistry struct {
	mu       sync.Mutex
	sessions map[string]*persistTerm
}

// persistTerm, tarayicidan bagimsiz yasayan tek bir uzak kabuk.
type persistTerm struct {
	id       string
	tenantID string
	clientID string
	owner    string
	tun      *tunnel.Session

	mu         sync.Mutex
	buf        []byte
	sub        chan []byte // bagli WS (yoksa nil)
	subExit    chan protocol.TerminalExit
	detachedAt time.Time
	exited     *protocol.TerminalExit
	closed     bool
	stop       context.CancelFunc
}

func (s *Server) termReg() *termRegistry {
	termRegInit.Lock()
	defer termRegInit.Unlock()
	if s.terms == nil {
		s.terms = &termRegistry{sessions: map[string]*persistTerm{}}
		go s.terms.janitor(s.Hub)
	}
	return s.terms
}

var termRegInit sync.Mutex

// termOwner, oturumu acan kimligi dondurur (yeniden baglanma bu kimlige ozel).
func termOwner(r *http.Request) string {
	if u, ok := userFromContext(r.Context()); ok && u.ID != "" {
		return "u:" + u.ID
	}
	return "key"
}

// lookup, sahibine ait canli oturumu bulur.
func (tr *termRegistry) lookup(id, tenantID, clientID, owner string) *persistTerm {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	t := tr.sessions[id]
	if t == nil || t.tenantID != tenantID || t.clientID != clientID || t.owner != owner {
		return nil
	}
	return t
}

func (tr *termRegistry) countFor(tenantID, clientID, owner string) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	n := 0
	for _, t := range tr.sessions {
		if t.tenantID == tenantID && t.clientID == clientID && t.owner == owner {
			n++
		}
	}
	return n
}

// open, istemcide yeni kabuk acar ve kayda ekler.
func (tr *termRegistry) open(tun *tunnel.Session, id, tenantID, clientID, owner, shell string) (*persistTerm, error) {
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan protocol.TerminalOutput, 256)
	exit := make(chan protocol.TerminalExit, 1)
	if err := tun.OpenTerminal(ctx, id, shell, 80, 24, out, exit); err != nil {
		cancel()
		tun.CloseTerminal(context.Background(), id)
		return nil, err
	}
	t := &persistTerm{
		id: id, tenantID: tenantID, clientID: clientID, owner: owner, tun: tun,
		detachedAt: time.Now(), stop: cancel,
	}
	tr.mu.Lock()
	tr.sessions[id] = t
	tr.mu.Unlock()
	go t.pump(ctx, out, exit)
	return t, nil
}

// pump, istemci ciktisini tampona yazar ve bagli WS varsa ona iletir.
func (t *persistTerm) pump(ctx context.Context, out <-chan protocol.TerminalOutput, exit <-chan protocol.TerminalExit) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-out:
			t.mu.Lock()
			t.buf = append(t.buf, m.Data...)
			if over := len(t.buf) - termBufferMax; over > 0 {
				t.buf = append(t.buf[:0:0], t.buf[over:]...)
			}
			if t.sub != nil {
				select {
				case t.sub <- m.Data:
				default: // yavas tarayici akisi bloklamasin; tamponda duruyor
				}
			}
			t.mu.Unlock()
		case m := <-exit:
			t.mu.Lock()
			t.exited = &m
			if t.subExit != nil {
				select {
				case t.subExit <- m:
				default:
				}
			}
			t.mu.Unlock()
			return
		}
	}
}

// attach, WS'i oturuma baglar: once tampon dondurulur, sonra canli akis kanali.
// Onceki bagli WS varsa ondan koparilir (oturumu en son acan sekme alir).
func (t *persistTerm) attach() (replay []byte, data chan []byte, ex chan protocol.TerminalExit, exited *protocol.TerminalExit) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sub != nil {
		close(t.sub)
	}
	t.sub = make(chan []byte, 256)
	t.subExit = make(chan protocol.TerminalExit, 1)
	replay = append([]byte(nil), t.buf...)
	return replay, t.sub, t.subExit, t.exited
}

// detach, WS koptugunda cagrilir; oturum termDetachTTL boyunca yasar.
func (t *persistTerm) detach(data chan []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sub != data {
		return // arada baska sekme baglandi
	}
	close(t.sub)
	t.sub, t.subExit = nil, nil
	t.detachedAt = time.Now()
}

// kill, kabugu istemcide kapatir ve kayittan siler.
func (tr *termRegistry) kill(t *persistTerm) {
	tr.mu.Lock()
	delete(tr.sessions, t.id)
	tr.mu.Unlock()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	t.stop()
	t.tun.CloseTerminal(context.Background(), t.id)
}

// janitor, uzun suredir bagsiz, kabugu bitmis veya istemcisi kopmus oturumlari kapatir.
func (tr *termRegistry) janitor(hub *tunnel.Hub) {
	tk := time.NewTicker(termJanitorEvery)
	defer tk.Stop()
	for range tk.C {
		tr.mu.Lock()
		all := make([]*persistTerm, 0, len(tr.sessions))
		for _, t := range tr.sessions {
			all = append(all, t)
		}
		tr.mu.Unlock()
		for _, t := range all {
			t.mu.Lock()
			detached := t.sub == nil
			idle := detached && time.Since(t.detachedAt) > termDetachTTL
			ended := detached && t.exited != nil
			t.mu.Unlock()
			cur, ok := hub.Get(t.clientID)
			gone := !ok || cur != t.tun // istemci yeniden baglandiysa eski kabuklar oldu
			if idle || ended || gone {
				tr.kill(t)
			}
		}
	}
}
