// Package accesslog, tunel ziyaretci erisim olaylarini (Basic Auth / OAuth giris
// denemeleri) tanimlar ve kalici depoya toplu, bloklamadan yazar.
package accesslog

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"
)

// Yontem ve neden sabitleri.
const (
	MethodBasic = "basic"
	MethodOAuth = "oauth"
	// MethodDoor, ham baglantiyla ilgili (giris yontemi olmayan) web-door olaylari.
	MethodDoor = "door"

	ReasonOK                    = "ok"
	ReasonBadCredentials        = "bad_credentials"
	ReasonEmailNotAllowed       = "email_not_allowed"
	ReasonRateLimited           = "rate_limited"
	ReasonProviderNotConfigured = "provider_not_configured"

	// Web ile kapi acma (ham TCP/UDP tunelleri).
	ReasonDoorOpened     = "door_opened"      // giris basarili, IP icin grant olustu
	ReasonDoorClosed     = "door_closed"      // ziyaretci kapiyi kapatti / sahip grant'i iptal etti
	ReasonDoorClosedPlan = "door_closed_plan" // plan Pro altina dustu: kapi otomatik kapandi, grant iptal edildi
	ReasonBlockedNoGrant = "blocked_no_grant" // grant'siz ham baglanti/akis reddedildi (toplulastirilmis)
)

// MaxUserAgent, saklanan User-Agent uzunlugu siniri.
const MaxUserAgent = 300

// Event, tek bir erisim olayi. Parola ASLA tasinmaz.
type Event struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id,omitempty"`
	TunnelID  string    `json:"tunnel_id"`
	Hostname  string    `json:"hostname,omitempty"`
	Method    string    `json:"method"`
	Provider  string    `json:"provider,omitempty"`
	Identity  string    `json:"identity,omitempty"`
	Success   bool      `json:"success"`
	Reason    string    `json:"reason,omitempty"`
	ClientIP  string    `json:"client_ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Count, toplulastirilmis olaylarda (blocked_no_grant) tek satirin temsil ettigi
	// olay sayisi. 0 veya 1 = tek olay.
	Count int `json:"count,omitempty"`
}

// Summary, bir zaman penceresindeki ozet sayaclar.
type Summary struct {
	Window           string           `json:"window"`
	Since            time.Time        `json:"since"`
	Total            int64            `json:"total"`
	Success          int64            `json:"success"`
	Failure          int64            `json:"failure"`
	ByMethod         map[string]int64 `json:"by_method"`
	ByProvider       map[string]int64 `json:"by_provider"`
	ByReason         map[string]int64 `json:"by_reason"`
	UniqueIdentities int64            `json:"unique_identities"`
	UniqueIPs        int64            `json:"unique_ips"`
	// Web-door: penceredeki acilan kapi (grant) sayisi ve grant'siz reddedilen
	// baglanti/akis sayisi. Bunlar Total/Success/Failure icine KATILMAZ.
	DoorGrants     int64 `json:"door_grants"`
	DoorBlocked    int64 `json:"door_blocked"`
	DoorBlockedIPs int64 `json:"door_blocked_ips"`
}

// NewID, rastgele (sayactan degil) bir olay kimligi uretir; yeniden baslatmada
// carpisma olmaz (bkz. reqlog.NewID).
func NewID() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("ae_t%x", time.Now().UnixNano())
	}
	return "ae_" + hex.EncodeToString(b[:])
}

// TruncateUA, User-Agent'i MaxUserAgent bayta (rune sinirinda) keser.
func TruncateUA(ua string) string {
	if len(ua) <= MaxUserAgent {
		return ua
	}
	cut := MaxUserAgent
	for cut > 0 && (ua[cut]&0xC0) == 0x80 {
		cut--
	}
	return ua[:cut]
}

// Persister, olaylari istek yolunu bloklamadan toplu yazar (reqlog.Persister ile ayni mantik).
type Persister struct {
	ch    chan Event
	flush func(ctx context.Context, batch []Event) error
	log   *slog.Logger

	batchSize int
	interval  time.Duration
}

// NewPersister, flush geri cagrimi (genelde store.InsertAccessEvents) ile persister uretir.
func NewPersister(flush func(ctx context.Context, batch []Event) error, log *slog.Logger) *Persister {
	return &Persister{
		ch:        make(chan Event, 2048),
		flush:     flush,
		log:       log,
		batchSize: 100,
		interval:  3 * time.Second,
	}
}

// Enqueue non-blocking'dir; kuyruk doluysa olay dusurulur. nil guvenlidir.
func (p *Persister) Enqueue(e Event) {
	if p == nil {
		return
	}
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.UserAgent = TruncateUA(e.UserAgent)
	select {
	case p.ch <- e:
	default:
	}
}

// Run, ctx iptal edilene kadar kuyrugu tuketir; kapanirken kalani yazar.
func (p *Persister) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	batch := make([]Event, 0, p.batchSize)
	doFlush := func() {
		if len(batch) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := p.flush(fctx, batch); err != nil && p.log != nil {
			p.log.Warn("erisim olaylari yazilamadi", "adet", len(batch), "hata", err)
		}
		cancel()
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case e := <-p.ch:
					batch = append(batch, e)
					if len(batch) >= p.batchSize {
						doFlush()
					}
				default:
					doFlush()
					return
				}
			}
		case e := <-p.ch:
			batch = append(batch, e)
			if len(batch) >= p.batchSize {
				doFlush()
			}
		case <-ticker.C:
			doFlush()
		}
	}
}
