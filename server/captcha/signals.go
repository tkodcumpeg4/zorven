package captcha

import (
	"regexp"
	"time"
)

// Signals, widget'in cozumle birlikte yolladigi basit ortam/etkilesim sinyalleri.
// KISISEL VERI YOK: yalniz sayac/bayrak; cerez, parmak izi veya kalici kimlik yok.
type Signals struct {
	WD bool `json:"wd"` // navigator.webdriver
	HL int  `json:"hl"` // headless ipucu sayisi (0-4): UA'da Headless, Chrome UA'sinda window.chrome yok, outerWidth/Height=0, diller bos
	MS int  `json:"ms"` // eksik tarayici API sayisi (fetch, Promise, crypto.getRandomValues, URL, Blob)
	MV int  `json:"mv"` // pointer hareketi olay sayisi (ust sinirli)
	KD int  `json:"kd"` // tus olayi
	TC int  `json:"tc"` // dokunma olayi
	FC int  `json:"fc"` // odak olayi
	SC int  `json:"sc"` // kaydirma olayi
	T  int  `json:"t"`  // istemcinin olctugu cozum suresi (ms)
}

// Puanlama esikleri (BELGELI; docs/captcha.md). Tek basina zayif hicbir sinyal
// reddi tetiklemez; yalnizca kesin otomasyon isaretleri (webdriver, headless UA)
// ya da birkac zayif isaretin toplami RejectScore'a ulasir.
const (
	// RaiseScore: sorgu asamasinda bu puan ve ustu zorlugu RaiseBits kadar artirir.
	RaiseScore = 40
	RaiseBits  = 2
	// MaxBits, artirilmis zorlugun ust siniri (site ayari 14-22 araligindadir).
	MaxBits = 24
	// RejectScore: cozum asamasinda bu puan ve ustu reddedilir (403 automated).
	RejectScore = 100
)

// Sinyal agirliklari.
const (
	wNoSignals     = 50  // govdede signals yok (ham HTTP istemcisi)
	wNoUA          = 50  // bos User-Agent
	wBotUA         = 100 // bilinen otomasyon/kutuphane UA'si
	wWebdriver     = 100 // navigator.webdriver === true
	wHeadless3     = 100 // 3+ headless ipucu
	wHeadless2     = 60
	wHeadless1     = 20
	wMissingAPI2   = 40 // 2+ eksik API
	wMissingAPI1   = 15
	wNoInteraction = 15 // hic pointer/tus/dokunma/odak/kaydirma olayi yok (sayfa yeni acilmis gercek kullanici da olabilir: dusuk agirlik)
	wNoAcceptLang  = 10 // Accept-Language basligi yok
	wTooFast       = 40 // sorgu ile cozum arasi < MinSolveGap
	wTimeMismatch  = 30 // istemci suresi sunucu olcumuyle celisiyor
)

// MinSolveGap, sorgudan cozume bu surenin altinda gelen cozum "cok hizli" sayilir
// (yalniz ag gidis-donusu bile bunu asar).
const MinSolveGap = 30 * time.Millisecond

var botUARe = regexp.MustCompile(`(?i)(headlesschrome|phantomjs|puppeteer|playwright|selenium|webdriver|python-requests|python-urllib|aiohttp|curl/|wget/|go-http-client|node-fetch|axios/|okhttp|libwww-perl|java/|scrapy|httpclient)`)

// ScoreInput, Score'un girdisi.
type ScoreInput struct {
	Signals    *Signals
	UserAgent  string
	AcceptLang bool          // Accept-Language basligi var mi
	Elapsed    time.Duration // sorgu -> cozum (sunucu olcumu); sorgu asamasinda 0
	Solve      bool          // true: cozum asamasi (etkilesim/sure kurallari uygulanir)
}

// Score, toplam puani ve tetiklenen kural adlarini doner.
func Score(in ScoreInput) (int, []string) {
	score := 0
	var why []string
	add := func(w int, name string) { score += w; why = append(why, name) }

	switch {
	case in.UserAgent == "":
		add(wNoUA, "no_ua")
	case botUARe.MatchString(in.UserAgent):
		add(wBotUA, "bot_ua")
	}
	if !in.AcceptLang {
		add(wNoAcceptLang, "no_accept_language")
	}
	s := in.Signals
	if s == nil {
		add(wNoSignals, "no_signals")
		return score, why
	}
	if s.WD {
		add(wWebdriver, "webdriver")
	}
	switch {
	case s.HL >= 3:
		add(wHeadless3, "headless")
	case s.HL == 2:
		add(wHeadless2, "headless_hints")
	case s.HL == 1:
		add(wHeadless1, "headless_hint")
	}
	switch {
	case s.MS >= 2:
		add(wMissingAPI2, "missing_apis")
	case s.MS == 1:
		add(wMissingAPI1, "missing_api")
	}
	if in.Solve {
		if s.MV+s.KD+s.TC+s.FC+s.SC == 0 {
			add(wNoInteraction, "no_interaction")
		}
		if in.Elapsed < MinSolveGap {
			add(wTooFast, "too_fast")
		}
		el := int(in.Elapsed / time.Millisecond)
		if s.T < 0 || s.T > el+1500 {
			add(wTimeMismatch, "time_mismatch")
		}
	}
	return score, why
}
