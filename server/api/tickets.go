package api

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ticketTTL, terminal biletinin gecerlilik suresi. Kisa: bilet yalnizca REST
// cagrisi ile WS baglantisi arasindaki birkac saniyeyi kapatmali.
const ticketTTL = 30 * time.Second

// TicketInfo, biletin kime ve hangi istemciye ait oldugunu tutar.
type TicketInfo struct {
	TenantID  string
	ClientID  string
	ExpiresAt time.Time
	Release   func()
}

// TicketStore, terminal ve ekran WS'leri icin tek kullanimlik biletleri tutar.
//
// NEDEN GEREKLI: tarayici WebSocket'te Authorization basligi GONDEREMEZ. Admin
// middleware bearer bekledigi icin WS baglantisi 401 alirdi. Bunun yerine
// dashboard once admin anahtari veya oturumla (REST, bearer/cookie) bir bilet alir,
// sonra WS'i ?ticket=... ile acar. Bilet tek kullanimlik, 30 saniye omurlu ve
// kiraci + istemci ile kriptografik olarak baglidir.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]TicketInfo
}

func NewTicketStore() *TicketStore {
	ts := &TicketStore{tickets: make(map[string]TicketInfo)}
	go ts.janitor()
	return ts
}

// issue, yeni bir bilet uretir ve kiraci/istemciye baglar.
func (ts *TicketStore) issue(tenantID, clientID string) string {
	return ts.issueWithRelease(tenantID, clientID, nil)
}

// issueWithRelease, slot tahsis fonksiyonu ile yeni bir bilet uretir.
func (ts *TicketStore) issueWithRelease(tenantID, clientID string, release func()) string {
	b := make([]byte, 24)
	rand.Read(b)
	tok := hex.EncodeToString(b)

	ts.mu.Lock()
	ts.tickets[tok] = TicketInfo{
		TenantID:  tenantID,
		ClientID:  clientID,
		ExpiresAt: time.Now().Add(ticketTTL),
		Release:   release,
	}
	ts.mu.Unlock()
	return tok
}

// redeem, bileti dogrular ve TEK KULLANIMLIK oldugu icin siler.
func (ts *TicketStore) redeem(tok string, expectedTenantID, expectedClientID string) (string, bool) {
	tenantID, _, ok := ts.redeemWithRelease(tok, expectedTenantID, expectedClientID)
	return tenantID, ok
}

// redeemWithRelease, bileti dogrular, siler ve varsa bagli release fonksiyonunu doner.
func (ts *TicketStore) redeemWithRelease(tok string, expectedTenantID, expectedClientID string) (string, func(), bool) {
	if tok == "" {
		return "", nil, false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()

	info, ok := ts.tickets[tok]
	if !ok {
		return "", nil, false
	}
	delete(ts.tickets, tok) // tek kullanimlik

	if time.Now().After(info.ExpiresAt) {
		if info.Release != nil {
			info.Release()
		}
		return "", nil, false
	}
	if info.TenantID != "" && expectedTenantID != "" && info.TenantID != expectedTenantID {
		if info.Release != nil {
			info.Release()
		}
		return "", nil, false
	}
	if info.ClientID != "" && expectedClientID != "" && info.ClientID != expectedClientID {
		if info.Release != nil {
			info.Release()
		}
		return "", nil, false
	}
	return info.TenantID, info.Release, true
}

func (ts *TicketStore) janitor() {
	tk := time.NewTicker(ticketTTL)
	defer tk.Stop()
	for range tk.C {
		now := time.Now()
		ts.mu.Lock()
		for k, info := range ts.tickets {
			if now.After(info.ExpiresAt) {
				if info.Release != nil {
					info.Release()
				}
				delete(ts.tickets, k)
			}
		}
		ts.mu.Unlock()
	}
}
