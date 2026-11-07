package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// terminalManager, istemcideki uzak kabuk oturumlarini yonetir.
//
// GUVENLIK: buradaki her oturum istemci makinesinde bir KABUK calistirir.
// Yetkilendirme sunucu tarafinda yapilir (yalnizca admin anahtarli dashboard
// terminal acabilir); istemci, sunucudan gelen komutlara guvenir cunku sunucuya
// zaten kendi token'iyla kimlik dogrulamis ve TLS uzerinden baglidir.
type terminalManager struct {
	cs *clientSession

	maxSessions int // es zamanli oturum ust siniri (0 => maxTerminalSessions)

	mu        sync.Mutex
	sessions  map[string]*terminalSession
	onChanged func() // oturum sayisi degistiginde cagrilir (nil olabilir)

	shells    shellSet // ajanin kesfettigi kabuklar (izin listesi)
	plainZsh  bool     // true: Zorven zsh profili kullanilmaz
	plainBash bool     // true: Zorven bash rcfile'i kullanilmaz
}

const (
	// maxTerminalSessions, ajan basina es zamanli kabuk oturumu siniri.
	maxTerminalSessions = 8
	// pump toplama ayarlari: kisa surede gelen okumalar tek cerceve olur.
	termCoalesceWindow = 10 * time.Millisecond
	termCoalesceMax    = 32 * 1024
	// termKillGrace, SIGHUP'tan sonra SIGKILL'e kadar taninan sure.
	termKillGrace = time.Second
)

type terminalSession struct {
	id   string
	pty  pty.Pty
	cmd  *pty.Cmd
	once sync.Once
}

func newTerminalManager(cs *clientSession, shells shellSet, plainZsh, plainBash bool) *terminalManager {
	return &terminalManager{cs: cs, sessions: make(map[string]*terminalSession),
		shells: shells, plainZsh: plainZsh, plainBash: plainBash}
}

// count, aktif terminal oturumu sayisini doner.
func (tm *terminalManager) count() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.sessions)
}

// open, yeni bir kabuk oturumu baslatir ve ciktisini sunucuya akitir.
func (tm *terminalManager) open(ctx context.Context, msg protocol.TerminalOpen) {
	// GUVENLIK: yalnizca ajanin kendi kesfettigi kabuklar kabul edilir; sunucudan
	// gelen "shell" bir yol degil, o listedeki bir ID'dir.
	sh, ok := tm.shells.find(msg.Shell)
	if !ok {
		tm.sendExit(ctx, msg.SessionID, 1, errUnknownShell.Error()+": "+msg.Shell)
		return
	}
	if tm.full() {
		tm.sendExit(ctx, msg.SessionID, 1, "terminal oturum siniri doldu")
		return
	}

	p, err := pty.New()
	if err != nil {
		tm.sendExit(ctx, msg.SessionID, 1, "pty acilamadi: "+err.Error())
		return
	}

	plan := prepareLaunch(sh, tm.plainZsh, tm.plainBash)
	cmd := p.Command(plan.Path, plan.Args[1:]...)
	cmd.Env = plan.Env
	cmd.Dir = plan.Dir
	if err := cmd.Start(); err != nil {
		p.Close()
		tm.sendExit(ctx, msg.SessionID, 1, "kabuk baslatilamadi: "+err.Error())
		return
	}

	if msg.Cols > 0 && msg.Rows > 0 {
		_ = p.Resize(int(msg.Cols), int(msg.Rows))
	}

	ts := &terminalSession{id: msg.SessionID, pty: p, cmd: cmd}
	tm.mu.Lock()
	if len(tm.sessions) >= tm.limit() { // acilis yarisi: sinir yeniden kontrol
		tm.mu.Unlock()
		ts.shutdown()
		tm.sendExit(ctx, msg.SessionID, 1, "terminal oturum siniri doldu")
		return
	}
	tm.sessions[msg.SessionID] = ts
	cb := tm.onChanged
	tm.mu.Unlock()
	if cb != nil {
		cb()
	}

	// Ciktiyi oku ve sunucuya gonder.
	go tm.pump(ctx, ts)

	// Kabuk bitince temizle ve sunucuya bildir.
	go func() {
		err := cmd.Wait()
		tm.finish(ctx, ts, exitCodeOf(cmd, err), "")
	}()
}

// exitCodeOf, kabugun gercek cikis kodunu doner (sinyalle olmus => 128+sinyal).
func exitCodeOf(cmd *pty.Cmd, err error) int {
	if cmd != nil && cmd.ProcessState != nil {
		if c := cmd.ProcessState.ExitCode(); c >= 0 {
			return c
		}
		return signalExitCode(cmd.ProcessState)
	}
	if err != nil {
		var ee interface{ ExitCode() int }
		if errors.As(err, &ee) && ee.ExitCode() >= 0 {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

func (tm *terminalManager) limit() int {
	if tm.maxSessions > 0 {
		return tm.maxSessions
	}
	return maxTerminalSessions
}

func (tm *terminalManager) full() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.sessions) >= tm.limit()
}

// pump, PTY ciktisini toplayarak sunucuya iletir. Okuyucu goroutine sinirli
// kanala yazar; gonderim (sendControl) esenkron bloklar, kanal dolunca okuyucu
// de bloklanir => bellek sinirsiz buyumez, veri dusmez.
// Cikis kodunu Wait goroutine'i raporlar; burada yalniz kaynaklar birakilir.
func (tm *terminalManager) pump(ctx context.Context, ts *terminalSession) {
	chunks := make(chan []byte, 4)
	stop := make(chan struct{})
	defer func() {
		close(stop)
		go ts.shutdown()
	}()

	go func() {
		defer close(chunks)
		buf := make([]byte, 8192)
		for {
			n, err := ts.pty.Read(buf)
			if n > 0 {
				out := make([]byte, n)
				copy(out, buf[:n])
				select {
				case chunks <- out:
				case <-stop:
					return
				}
			}
			if err != nil {
				return // EOF veya kabuk kapandi
			}
		}
	}()

	for first := range chunks {
		batch := first
		timer := time.NewTimer(termCoalesceWindow)
	collect:
		for len(batch) < termCoalesceMax {
			select {
			case more, ok := <-chunks:
				if !ok {
					break collect
				}
				batch = append(batch, more...)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		if serr := tm.cs.sendControl(ctx, protocol.TerminalOutput{
			Type:      protocol.TypeTerminalOutput,
			SessionID: ts.id,
			Data:      batch,
		}, protocol.TypeTerminalOutput); serr != nil {
			return
		}
	}
}

func (tm *terminalManager) input(msg protocol.TerminalInput) {
	tm.mu.Lock()
	ts, ok := tm.sessions[msg.SessionID]
	tm.mu.Unlock()
	if ok {
		ts.pty.Write(msg.Data)
	}
}

func (tm *terminalManager) resize(msg protocol.TerminalResize) {
	tm.mu.Lock()
	ts, ok := tm.sessions[msg.SessionID]
	tm.mu.Unlock()
	if ok && msg.Cols > 0 && msg.Rows > 0 {
		ts.pty.Resize(int(msg.Cols), int(msg.Rows))
	}
}

func (tm *terminalManager) closeSession(sessionID string) {
	tm.mu.Lock()
	ts, ok := tm.sessions[sessionID]
	delete(tm.sessions, sessionID)
	cb := tm.onChanged
	tm.mu.Unlock()
	if ok {
		go ts.shutdown()
		if cb != nil {
			cb()
		}
	}
}

// finish, oturumu kayittan dusurur, kaynaklari kapatir ve sunucuya exit bildirir.
func (tm *terminalManager) finish(ctx context.Context, ts *terminalSession, code int, msg string) {
	tm.mu.Lock()
	_, ok := tm.sessions[ts.id]
	delete(tm.sessions, ts.id)
	cb := tm.onChanged
	tm.mu.Unlock()
	if !ok {
		return // zaten temizlenmis
	}
	go ts.shutdown()
	tm.sendExit(ctx, ts.id, code, msg)
	if cb != nil {
		cb()
	}
}

func (tm *terminalManager) sendExit(ctx context.Context, id string, code int, msg string) {
	_ = tm.cs.sendControl(ctx, protocol.TerminalExit{
		Type:      protocol.TypeTerminalExit,
		SessionID: id,
		Code:      code,
		Message:   msg,
	}, protocol.TypeTerminalExit)
}

// closeAll, baglanti koptugunda tum kabuk oturumlarini sonlandirir.
// Bu olmadan kopan bir baglanti arkada zombi kabuk surecleri birakirdi.
func (tm *terminalManager) closeAll() {
	tm.mu.Lock()
	all := tm.sessions
	tm.sessions = make(map[string]*terminalSession)
	cb := tm.onChanged
	tm.mu.Unlock()
	for _, ts := range all {
		ts.shutdown()
	}
	if cb != nil {
		cb()
	}
}

func (ts *terminalSession) shutdown() {
	ts.once.Do(func() {
		if ts.cmd != nil && ts.cmd.Process != nil {
			killProcessTree(ts.cmd.Process, termKillGrace)
		}
		ts.pty.Close()
	})
}
