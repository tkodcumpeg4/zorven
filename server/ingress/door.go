package ingress

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Web ile kapi acma (web door) ziyaretci arayuzu.
//
// Kapi hostname'i (door--<ad>--<kiraci>.<platform>) router'da HostRoute.Door != nil
// ile isaretlidir. Bu host'a gelen istekler tunele proxylenmez:
//
//	/_zva/*       OAuth giris uclari (mevcut visitorauth)
//	/_zvb/*       Basic giris formu uclari (mevcut)
//	/_zvd/close   POST: ziyaretcinin kendi IP'si icin kapiyi kapat
//	/             giris sayfasi; giris varsa grant acar ve "kapi acildi" sayfasini gosterir
//
// Giris dogrulamasi tunelin MEVCUT erisim politikasini (basic/oauth) kullanir.

const doorClosePath = "/_zvd/close"

// serveDoor, kapi hostname'ine gelen istegi ele alir.
func (h *Handler) serveDoor(w http.ResponseWriter, r *http.Request, tun store.HostRoute) {
	if h.Door == nil {
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound, "Bulunamadi.")
		return
	}
	lang := pickLang(r)
	t := pageTexts[lang]

	// Plan kapisi: kiracinin plani web kapiyi icermiyorsa (kapi plan nedeniyle
	// kapatilmis ya da hic acilamaz) giris sunulmaz, bilgi sayfasi gosterilir.
	if !h.Door.PlanAllowed(tun.TenantID) {
		h.renderDoorUnavailable(w, r, t.doorUnavailTitle, t.doorUnavailMsg, lang)
		return
	}
	// Kapi kapali (ornegin plan nedeniyle kalici kapatilmisti, plan simdi yeterli):
	// kapi yok, eski davranis.
	if tun.Door != nil && tun.Door.Disabled {
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound, "Bulunamadi.")
		return
	}

	// Giris uclari erisim denetiminden muaftir (kimlik dogrulamanin kendisi).
	if h.Visitor != nil && h.Visitor.Enabled() && visitorauth.HandlesPath(r.URL.Path) {
		h.Visitor.ServeTunnelEndpoint(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_zvb/") {
		h.serveBasicEndpoint(w, r, tun)
		return
	}

	// Giris yontemi yapilandirilmamissa (none/kapali) kapi acilamaz: fail-closed.
	if !tun.AccessEnabled || (tun.AccessMode != "basic" && tun.AccessMode != "oauth") {
		h.renderDoorUnavailable(w, r, t.unconfTitle, t.unconfMsg, lang)
		return
	}

	isClose := r.URL.Path == doorClosePath
	if !isClose && r.URL.Path != "/" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if isClose && r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !isClose && r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, protocol.CodeAccessDenied, "Yontem desteklenmiyor.")
		return
	}
	// Kapatma istegi: baska siteden tetiklenemesin (Origin varsa ayni host olmali).
	if isClose {
		if o := r.Header.Get("Origin"); o != "" && o != "null" {
			if u, err := url.Parse(o); err != nil || !strings.EqualFold(normalizeHost(u.Host), normalizeHost(r.Host)) {
				writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied, "Gecersiz istek kaynagi.")
				return
			}
		}
	}

	// Oturum iptali: grant iptal edilen (ya da kapiyi kapatan) kisinin ESKI oturum
	// cerezi kapiyi yeniden acamaz; cerezler silinir, giris sayfasi gosterilir.
	if h.doorSessionRevoked(r, tun) {
		clearDoorCookies(w)
		r = withoutSessionCookies(r)
	}

	// Giris denetimi: login sayfasi / yonlendirme enforceAccess tarafindan yazilir.
	if !h.enforceAccess(w, r, tun) {
		return
	}

	identity, method := h.doorIdentity(r, tun)
	ip := clientIP(r)
	target := door.Target{
		TenantID: tun.TenantID, TunnelID: tun.TunnelID,
		Host: normalizeHost(r.Host), DurationSec: tun.Door.DurationSec,
	}

	if isClose {
		if _, err := h.Door.Close(r.Context(), target, ip, identity, method, r.UserAgent()); err != nil {
			h.Log.Warn("door: kapi kapatilamadi", "tunel", tun.TunnelID, "hata", err)
			writeError(w, r, http.StatusInternalServerError, protocol.CodeAccessDenied, "Kapi kapatilamadi.")
			return
		}
		// Oturum cerezlerini sil: tekrar girmek icin yeniden giris gerekir.
		clearDoorCookies(w)
		renderAccessPage(w, r, http.StatusOK, accessPageData{
			Lang: lang, Code: "door_closed", Kind: pageDoorClosed,
			Heading: t.doorClosedTitle, Message: t.doorClosedMsg,
			CloseHref: "/", ReopenText: t.doorReopen,
		})
		return
	}

	// HEAD kapi acmaz (yan etkisiz).
	if r.Method == http.MethodHead {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}

	g, _, err := h.Door.Open(r.Context(), target, ip, identity, method, r.UserAgent())
	if err != nil {
		if errors.Is(err, door.ErrNotAvailable) {
			h.renderDoorUnavailable(w, r, t.doorUnavailTitle, t.doorUnavailMsg, lang)
			return
		}
		h.Log.Warn("door: kapi acilamadi", "tunel", tun.TunnelID, "hata", err)
		writeError(w, r, http.StatusInternalServerError, protocol.CodeAccessDenied, "Kapi acilamadi.")
		return
	}
	renderAccessPage(w, r, http.StatusOK, accessPageData{
		Lang: lang, Code: "door_open", Kind: pageDoorOpen,
		Heading: t.doorOpenTitle, Message: t.doorOpenMsg,
		Rows:      h.doorRows(tun, g, t, time.Now()),
		CloseText: t.doorClose,
	})
}

func (h *Handler) renderDoorUnavailable(w http.ResponseWriter, r *http.Request, title, msg, lang string) {
	renderAccessPage(w, r, http.StatusForbidden, accessPageData{
		Lang: lang, Code: protocol.CodeAccessDenied, Kind: pageUnconfigured,
		Heading: title, Message: msg,
	})
}

// doorIdentity, dogrulanmis istegin kimligini (kullanici adi / e-posta) ve yontemini doner.
func (h *Handler) doorIdentity(r *http.Request, tun store.HostRoute) (identity, method string) {
	switch tun.AccessMode {
	case "oauth":
		if h.Visitor != nil {
			if email, ok := h.Visitor.SessionEmail(r); ok {
				return truncateIdentity(email), accesslog.MethodOAuth
			}
		}
		return "", accesslog.MethodOAuth
	default:
		if user, _, ok := r.BasicAuth(); ok {
			return truncateIdentity(user), accesslog.MethodBasic
		}
		return truncateIdentity(parseBasicConfig(tun.AccessConfig).Username), accesslog.MethodBasic
	}
}

// doorSessionRevoked, istekteki Basic/OAuth oturum cerezi bu tunel icin iptal edilmis
// (verilisi son iptalden once/esit) mi. Cerez yoksa/gecersizse false (zaten giris istenir).
func (h *Handler) doorSessionRevoked(r *http.Request, tun store.HostRoute) bool {
	switch tun.AccessMode {
	case "oauth":
		if h.Visitor == nil {
			return false
		}
		email, issued, ok := h.Visitor.SessionInfo(r)
		if !ok {
			return false
		}
		return h.Door.SessionRevoked(tun.TunnelID, email, issued)
	case "basic":
		cfg := parseBasicConfig(tun.AccessConfig)
		issued, ok := h.basicCookieIssued(r, tun, cfg)
		if !ok {
			return false
		}
		return h.Door.SessionRevoked(tun.TunnelID, truncateIdentity(cfg.Username), issued)
	}
	return false
}

// withoutSessionCookies, istegin kopyasindan Zorven oturum cerezlerini cikarir.
func withoutSessionCookies(r *http.Request) *http.Request {
	r2 := r.Clone(r.Context())
	vals := stripAuthCookies(r.Header.Values("Cookie"))
	r2.Header.Del("Cookie")
	for _, v := range vals {
		r2.Header.Add("Cookie", v)
	}
	return r2
}

// clearDoorCookies, Basic ve OAuth ziyaretci oturum cerezlerini siler.
func clearDoorCookies(w http.ResponseWriter) {
	for _, name := range []string{basicCookie, "_zva_session"} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
	}
}

// doorConnectAddr, ziyaretciye gosterilen baglanti adresi (host:port).
func (h *Handler) doorConnectAddr(tun store.HostRoute) string {
	d := tun.Door
	if d == nil {
		return ""
	}
	if d.Exposure == store.ExposureSNI {
		if d.TunnelHost == "" {
			return ""
		}
		return d.TunnelHost + ":443"
	}
	host := h.PlatformDomain
	if host == "" {
		host = d.TunnelHost
	}
	if host == "" || d.PublicPort == 0 {
		return ""
	}
	return host + ":" + strconv.Itoa(d.PublicPort)
}

func (h *Handler) doorRows(tun store.HostRoute, g store.DoorGrant, t pageText, now time.Time) []doorRow {
	rows := []doorRow{{Label: t.doorIP, Value: g.IP}}
	if g.Identity != "" {
		rows = append(rows, doorRow{Label: t.doorIdentity, Value: g.Identity})
	}
	rows = append(rows, doorRow{
		Label: t.doorUntil,
		Value: g.ExpiresAt.UTC().Format("2006-01-02 15:04") + " UTC (" + remainingText(t, g.ExpiresAt.Sub(now)) + ")",
	})
	if addr := h.doorConnectAddr(tun); addr != "" {
		rows = append(rows, doorRow{Label: t.doorAddr, Value: addr})
	}
	if tun.Door != nil {
		proto := strings.ToUpper(tun.Door.Proto)
		if tun.Door.Exposure == store.ExposureSNI {
			proto += " / SNI"
		}
		rows = append(rows, doorRow{Label: t.doorProto, Value: proto})
	}
	return rows
}

// remainingText, kalan sureyi "11 sa 59 dk kaldi" bicimine cevirir.
func remainingText(t pageText, d time.Duration) string {
	if d < 0 {
		d = 0
	}
	mins := int(d.Minutes())
	if mins >= 24*60 {
		return fmt.Sprintf(t.doorRemainingDaysFmt, mins/(24*60), (mins%(24*60))/60)
	}
	return fmt.Sprintf(t.doorRemainingFmt, mins/60, mins%60)
}
