package ingress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// enforceOAuth, mode=oauth denetimi. true = gec.
//
// Tarayici (GET/HEAD + Accept: text/html) istekleri markali giris sayfasi gorur;
// diger istemciler eski davranisi korur (302 / 401 / 403 JSON).
func (h *Handler) enforceOAuth(w http.ResponseWriter, r *http.Request, tun store.HostRoute) bool {
	var cfg struct {
		Providers     []string `json:"providers"`
		AllowedEmails []string `json:"allowed_emails"`
	}
	_ = json.Unmarshal(tun.AccessConfig, &cfg)

	enabled := h.Visitor != nil && h.Visitor.Enabled()
	browser := wantsHTMLPage(r)
	lang := pickLang(r)
	t := pageTexts[lang]
	rd := r.URL.RequestURI()

	var email string
	var hasSession bool
	var buttons []providerButton
	if enabled {
		email, hasSession = h.Visitor.SessionEmail(r)
		if hasSession && visitorauth.EmailAllowed(email, cfg.AllowedEmails) {
			return true
		}
		buttons = buildProviderButtons(lang, h.Visitor.ProviderNames(), cfg.Providers, rd)
	}

	// Kullanilabilir saglayici yok: sunucu yapilandirmamis ya da tunelin izin verdigi
	// saglayicilarin hicbiri sunucuda tanimli degil. Fail-closed.
	if !enabled || len(buttons) == 0 {
		h.recordAccess(tun, r, accesslog.MethodOAuth, "", "", false, accesslog.ReasonProviderNotConfigured)
		if browser {
			renderAccessPage(w, r, http.StatusForbidden, accessPageData{
				Lang: lang, Code: protocol.CodeAccessDenied, Kind: pageUnconfigured,
				Heading: t.unconfTitle, Message: t.unconfMsg,
			})
			return false
		}
		writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
			"OAuth erisim denetimi bu sunucuda yapilandirilmamis (saglayici anahtarlari eksik).")
		return false
	}

	if hasSession {
		// Oturum var ama e-posta izin listesinde degil (olay callback'te kaydedildi).
		if browser {
			q := url.Values{}
			q.Set("rd", rd)
			renderAccessPage(w, r, http.StatusForbidden, accessPageData{
				Lang: lang, Code: protocol.CodeAccessDenied, Kind: pageDenied,
				Heading: t.deniedTitle, Message: fmt.Sprintf(t.deniedMsg, email),
				SwitchHref: "/_zva/logout?" + q.Encode(), SwitchLabel: t.switchAccount,
			})
			return false
		}
		writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
			"Erisim reddedildi: hesabiniz ("+email+") bu tunele erisim yetkisine sahip degil.")
		return false
	}

	// Oturum yok. GET/HEAD disinda 401 (tarayici olmayan istemci redirect izlemez).
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, r, http.StatusUnauthorized, protocol.CodeAuthRequired,
			"Bu tunel OAuth ile korunmaktadir; once tarayicidan giris yapin.")
		return false
	}
	if browser {
		renderAccessPage(w, r, http.StatusUnauthorized, accessPageData{
			Lang: lang, Code: protocol.CodeAuthRequired, Kind: pageOAuth,
			Heading: t.protectedTitle, Message: t.oauthHint, Providers: buttons,
		})
		return false
	}
	names := make([]string, 0, len(buttons))
	for _, b := range buttons {
		names = append(names, b.Name)
	}
	http.Redirect(w, r, visitorauth.StartURL(names, rd), http.StatusFound)
	return false
}

// RecordVisitorEvent, visitorauth.Manager.OnEvent icin: OAuth giris sonucunu
// (basari / izinsiz e-posta) erisim istatistiklerine yazar.
func (h *Handler) RecordVisitorEvent(ev visitorauth.Event) {
	if h.AccessEvents == nil || h.Router == nil {
		return
	}
	tun, ok := h.Router.Lookup(ev.Host)
	if !ok {
		return
	}
	h.AccessEvents.Enqueue(accesslog.Event{
		TenantID: tun.TenantID, TunnelID: tun.TunnelID, Hostname: ev.Host,
		Method: accesslog.MethodOAuth, Provider: ev.Provider, Identity: truncateIdentity(ev.Email),
		Success: ev.Success, Reason: ev.Reason, ClientIP: ev.ClientIP, UserAgent: ev.UserAgent,
	})
}
