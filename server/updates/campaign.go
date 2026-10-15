package updates

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Campaign, bir yazinin toplu duyuru e-postasini hiz sinirli kuyrukla gonderir.
type Campaign struct {
	Store   store.UpdatesStore
	Mailer  SendFunc
	Key     []byte
	BaseURL string // https://zorven.app
	From    string // "Zorven <no-reply@zorven.app>"
	Rate    float64
	// Mode: "" = yeni/tam gonderim ve devam (gonderilmis alicilar atlanir);
	// store.SendModeRetryFailed = yalniz status=failed olanlar yeniden denenir.
	Mode string
	Log  *slog.Logger
}

// SendFunc, tek e-posta gonderen islev (test icin degistirilebilir).
type SendFunc func(from, to, subject, text, html string, headers []string) error

var (
	runMu   sync.Mutex
	running = map[string]bool{}
)

// IsRunning, yazinin kuyrugu su an bu surecte calisiyor mu.
func IsRunning(postID string) bool {
	runMu.Lock()
	defer runMu.Unlock()
	return running[postID]
}

func (c *Campaign) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

// Run, yazinin e-postasini tum abonelere gonderir ve istatistikleri depoya yazar.
// Ayni yazi icin es zamanli ikinci Run reddedilir (false). Bloklar; goroutine'de
// cagrilmalidir. Sirayla: kullanicilar anahtar-sayfalamayla okunur, her biri
// icin tercih SON KEZ yeniden kontrol edilir (kuyruk beklerken cikanlara
// gonderilmez), hiz Rate/sn ile sinirlanir.
func (c *Campaign) Run(ctx context.Context, post store.UpdatePost, total int) bool {
	runMu.Lock()
	if running[post.ID] {
		runMu.Unlock()
		return false
	}
	running[post.ID] = true
	runMu.Unlock()
	defer func() {
		runMu.Lock()
		delete(running, post.ID)
		runMu.Unlock()
	}()

	rate := c.Rate
	if rate <= 0 {
		rate = 5
	}
	interval := time.Duration(float64(time.Second) / rate)
	tick := time.NewTicker(interval)
	defer tick.Stop()

	// Sayaclar kalici gunlukten baslar: devam/yeniden deneme toplami bozmaz.
	st := store.UpdateEmailStats{Total: total}
	if sent, failed, err := c.Store.CountUpdateDeliveries(ctx, post.ID); err == nil {
		st.Sent, st.Failed = sent, failed
	}
	// flushStats: done=true yalniz kuyruk GERCEKTEN bittiginde; aksi halde
	// interrupted (yonetici "Devam et" ile surdurur).
	flushStats := func(done, interrupted bool) {
		st.Done = done
		st.Interrupted = interrupted
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Store.SetUpdatePostEmailStats(sctx, post.ID, st); err != nil {
			c.log().Warn("duyuru istatistigi yazilamadi", "yazi", post.ID, "hata", err)
		}
	}
	retryOnly := c.Mode == store.SendModeRetryFailed

	after := ""
	for {
		batch, err := c.Store.ListAnnouncementRecipients(ctx, after, 200)
		if err != nil {
			c.log().Error("duyuru alicilari okunamadi", "yazi", post.ID, "hata", err)
			flushStats(false, true)
			return true
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, 0, len(batch))
		for _, r := range batch {
			ids = append(ids, r.UserID)
		}
		prior, err := c.Store.UpdateDeliveryStatuses(ctx, post.ID, ids)
		if err != nil {
			c.log().Error("gonderim gunlugu okunamadi", "yazi", post.ID, "hata", err)
			flushStats(false, true)
			return true
		}
		for _, rcpt := range batch {
			after = rcpt.UserID
			if !rcpt.Verified || strings.TrimSpace(rcpt.Email) == "" {
				continue
			}
			was := prior[rcpt.UserID]
			if was == store.DeliverySent || (retryOnly && was != store.DeliveryFailed) {
				continue
			}
			select {
			case <-ctx.Done():
				flushStats(false, true)
				return true
			case <-tick.C:
			}
			// Son kontrol: kuyruk beklerken abonelikten cikmis olabilir (opt-in).
			if sub, err := c.Store.IsAnnouncementSubscribed(ctx, rcpt.UserID); err != nil || !sub {
				continue
			}
			em := BuildEmail(post, rcpt.Lang, c.BaseURL, c.Key, rcpt.UserID)
			status, errMsg := store.DeliverySent, ""
			if err := c.Mailer(c.From, rcpt.Email, em.Subject, em.Text, em.HTML, em.Headers); err != nil {
				status, errMsg = store.DeliveryFailed, err.Error()
				c.log().Warn("duyuru e-postasi gonderilemedi", "yazi", post.ID, "kullanici", rcpt.UserID, "hata", err)
			}
			if err := c.Store.RecordUpdateDelivery(ctx, post.ID, rcpt.UserID, status, errMsg); err != nil {
				// Gunluk yazilamazsa devam etmek tekrar gonderime yol acar: durdur, devam edilebilir.
				c.log().Error("gonderim gunlugu yazilamadi", "yazi", post.ID, "hata", err)
				flushStats(false, true)
				return true
			}
			switch {
			case status == store.DeliverySent && was == store.DeliveryFailed:
				st.Failed--
				st.Sent++
			case status == store.DeliverySent:
				st.Sent++
			case was != store.DeliveryFailed:
				st.Failed++
			}
			if (st.Sent+st.Failed)%25 == 0 {
				flushStats(false, false)
			}
		}
	}
	flushStats(true, false)
	c.log().Info("duyuru gonderimi bitti", "yazi", post.ID, "gonderilen", st.Sent, "basarisiz", st.Failed)
	return true
}
