package agent

import (
	"context"
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// bodyGrant, HTTP istek govdesi kredisinin ajan tarafi (body_credit; tel
// bicimi shared/protocol/streamflow.go).
//
// NEDEN: istek govdesi yonunde akis kontrolu yoktu; sunucu ziyaretcinin
// hiziyla gonderiyor, ajanin 4 MB'lik kuyrugu yavas yerel serviste tasip
// yuklemeyi kesiyordu. Artik sunucu en fazla InitialWindowBytes kadar
// gonderir; ajan veriyi yerel servise yazdikca (ya da bilerek attikca) ayni
// birimle (len + StreamFrameCost) kredi iade eder. Kuyruk (4 MB / 1024 oge)
// pencereden (2 MB / en fazla 512 cerceve) buyuk oldugu icin kredi uyan
// sunucuda artik tasmaz.
//
// Iade esigi pencerenin yarisi: sunucu ancak bekleyen kredisi pencere eksi bir
// cerceveyi astiginda durur, bu da her zaman esigin ustundedir; dolayisiyla
// biriken iade hicbir zaman esigin altinda takili kalip sunucuyu bekletmez.
type bodyGrant struct {
	cs    *clientSession
	reqID uint64

	mu   sync.Mutex
	recv int  // alinmis, henuz iade edilmemis kredi
	done int  // yazilmis/atilmis, iade edilmeyi bekleyen kredi (done <= recv)
	dead bool // govde artik yazilmayacak: gelen her sey hemen "atilmis" sayilir
}

const reqGrantThreshold = protocol.InitialWindowBytes / 2

func reqFrameCost(n int) int { return n + protocol.StreamFrameCost }

// received, sunucudan govde cercevesi geldi (kuyruga alinmadan ONCE).
func (g *bodyGrant) received(n int) {
	g.mu.Lock()
	c := reqFrameCost(n)
	g.recv += c
	if g.dead {
		g.done += c
	}
	inc := g.takeLocked()
	g.mu.Unlock()
	g.send(inc)
}

// written, cerceve yerel servise yazildi (kuyruk goroutine'i).
func (g *bodyGrant) written(n int) {
	g.mu.Lock()
	if g.dead { // kill zaten tumunu iade etti
		g.mu.Unlock()
		return
	}
	g.done += reqFrameCost(n)
	inc := g.takeLocked()
	g.mu.Unlock()
	g.send(inc)
}

// kill, govdenin geri kalaninin yazilmayacagini bildirir (kuyruk durdu, yerel
// servis okumayi birakti, istek bitti/iptal): bekleyen tum kredi atilmis sayilir.
func (g *bodyGrant) kill() {
	g.mu.Lock()
	g.dead = true
	g.done = g.recv
	inc := g.takeLocked()
	g.mu.Unlock()
	g.send(inc)
}

func (g *bodyGrant) takeLocked() int {
	if g.done < reqGrantThreshold {
		return 0
	}
	inc := g.done
	g.recv -= inc
	g.done = 0
	return inc
}

// send, iadeyi ayri goroutine'de yollar: received okuma dongusunden cagrilir
// ve yazim beklemesi (grup yazici) okumayi bloklamamali. Iadelerin sirasi
// onemsizdir (toplanir).
func (g *bodyGrant) send(inc int) {
	if inc <= 0 || g.cs == nil {
		return
	}
	go func() {
		_ = g.cs.sendControl(context.Background(), protocol.WindowUpdate{
			Type: protocol.TypeWindowUpdate, ReqID: g.reqID, Increment: inc,
		}, protocol.TypeWindowUpdate)
	}()
}
