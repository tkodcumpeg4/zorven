package main

// Arka plan guncelleme denetimi ve OS bildirimleri.

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	updateFirstCheck = 30 * time.Second
	updateMinEvery   = 6 * time.Hour
	updateSpread     = 6 * time.Hour // 6-12 saat arasi
)

// nextUpdateDelay, bir sonraki arka plan denetimine kalan sure (6-12 saat).
func nextUpdateDelay() time.Duration {
	return updateMinEvery + time.Duration(rand.Int64N(int64(updateSpread)))
}

// updateLoop, acilista ve 6-12 saatte bir yeni surum arar; bulursa bloklamayan
// bir afis, tepsi ogesi ve (aciksa) OS bildirimi gosterir.
func (a *App) updateLoop(ctx context.Context) {
	if version == "dev" {
		return
	}
	delay := updateFirstCheck
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		a.backgroundCheck()
		delay = nextUpdateDelay()
	}
}

func (a *App) backgroundCheck() {
	info := a.CheckForUpdate()
	if info.Error != "" {
		a.log.Info("arka plan guncelleme denetimi basarisiz", "hata", info.Error)
		return
	}
	if !info.Available {
		if a.tray != nil {
			a.tray.setUpdate("")
		}
		emit(a.ctx, "update:available", info)
		return
	}
	emit(a.ctx, "update:available", info)
	if a.tray != nil {
		a.tray.setUpdate(info.Latest)
	}
	a.upd.mu.Lock()
	first := a.upd.notified != info.Latest
	a.upd.notified = info.Latest
	a.upd.mu.Unlock()
	if first {
		a.notify("update", tm("note.updateTitle"), tm("note.updateBody", info.Latest, info.Current))
	}
}

// notify, OS bildirimi gosterir (ayar kapaliysa ya da kullanilamiyorsa sessiz).
func (a *App) notify(id, title, body string) {
	if a.ctx == nil || !a.GetNotifications() {
		return
	}
	go func() {
		err := wailsrt.SendNotification(a.ctx, wailsrt.NotificationOptions{
			ID:    id + "-" + time.Now().Format("150405.000"),
			Title: title,
			Body:  body,
		})
		if err != nil {
			a.log.Debug("bildirim gonderilemedi", "hata", err)
		}
	}()
}

// notifyTransition, baglanti durumu gecislerinde bildirim verir: baglandi,
// koptu (nedenle), yeniden baglandi. Kullanicinin kendi kestigi baglanti
// (stopped) bildirim uretmez.
func (a *App) notifyTransition(prev agent.State, s agent.Status, wasUp bool) {
	if prev == s.State {
		return
	}
	switch s.State {
	case agent.StateConnected:
		if wasUp {
			a.notify("reconnected", tm("note.reconnected"), tm("note.reconnectedBody"))
		} else {
			a.notify("connected", tm("note.connected"), tm("note.connectedBody"))
		}
	case agent.StateReconnecting, agent.StateFatal:
		if prev == agent.StateConnected {
			a.notify("disconnected", tm("note.disconnected"), s.Message)
		}
	}
}
