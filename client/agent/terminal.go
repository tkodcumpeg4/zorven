package agent

import (
	"context"
	"os"
	"runtime"
	"sync"

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

	mu        sync.Mutex
	sessions  map[string]*terminalSession
	onChanged func() // oturum sayisi degistiginde cagrilir (nil olabilir)
}

type terminalSession struct {
	id   string
	pty  pty.Pty
	cmd  *pty.Cmd
	once sync.Once
}

func newTerminalManager(cs *clientSession) *terminalManager {
	return &terminalManager{cs: cs, sessions: make(map[string]*terminalSession)}
}

// count, aktif terminal oturumu sayisini doner.
func (tm *terminalManager) count() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.sessions)
}

// shellCommand, platforma uygun varsayilan kabugu doner.
func shellCommand() string {
	if runtime.GOOS == "windows" {
		if ps, ok := os.LookupEnv("COMSPEC"); ok && ps != "" {
			return ps // genelde cmd.exe
		}
		return "powershell.exe"
	}
	if sh, ok := os.LookupEnv("SHELL"); ok && sh != "" {
		return sh
	}
	return "/bin/sh"
}

// open, yeni bir kabuk oturumu baslatir ve ciktisini sunucuya akitir.
func (tm *terminalManager) open(ctx context.Context, msg protocol.TerminalOpen) {
	p, err := pty.New()
	if err != nil {
		tm.sendExit(ctx, msg.SessionID, 1, "pty acilamadi: "+err.Error())
		return
	}

	cmd := p.Command(shellCommand())
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
		code := 0
		if err != nil {
			code = 1
		}
		tm.finish(ctx, ts, code, "")
	}()
}

// pump, PTY ciktisini parca parca sunucuya iletir.
func (tm *terminalManager) pump(ctx context.Context, ts *terminalSession) {
	defer func() {
		go ts.shutdown()
		tm.finish(ctx, ts, 0, "")
	}()

	buf := make([]byte, 8192)
	for {
		n, err := ts.pty.Read(buf)
		if n > 0 {
			out := make([]byte, n)
			copy(out, buf[:n])
			if serr := tm.cs.sendControl(ctx, protocol.TerminalOutput{
				Type:      protocol.TypeTerminalOutput,
				SessionID: ts.id,
				Data:      out,
			}, protocol.TypeTerminalOutput); serr != nil {
				return
			}
		}
		if err != nil {
			return // EOF veya kabuk kapandi
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
			ts.cmd.Process.Kill()
		}
		ts.pty.Close()
	})
}
