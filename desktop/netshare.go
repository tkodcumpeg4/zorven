package main

// Zorven Net paylasim ve erisim sayfasi (CLI'deki `zorven share` / `zorven
// access` karsiligi). Islemler cihazdaki ajana yaptirilir, yeni bir ag kodu yok:
//
//   - Masaustu bagliyken kendi gomulu ajani (localctl.NetController'i dogrudan
//     uygular: ShareCreate/ShareDelete/ShareList/AccessOpen/AccessClose).
//   - Gomulu ajan yokken ayni makinedeki CLI ajani/servis (localctl IPC; CLI'deki
//     findRunning/callAgent yolu). IPC erisilebilir paylasim listesini sunmaz,
//     bu yuzden o kipte hedef elle yazilir.
//
// Davranis CLI ile aynidir: alici bicimleri (parseGrantee), sure en az 1 dk,
// gecici paylasim ajan oturumuyla biter (--persist ile kalici; sistem servisinde
// her zaman kalici), erisim yalniz 127.0.0.1'e baglanir ve yerel port bos
// birakilirsa paylasimin portunu kullanir.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Gomulu ajan localctl.NetController'i dogrudan saglar (CLI'de agentCtl sarmalayicisi
// ayni cagrilari ajana devreder).
var _ localctl.NetController = (*agent.Agent)(nil)

// netReadier, Zorven Net dugumunun hazir olmasini bekleyebilen denetleyici (gomulu ajan).
type netReadier interface {
	NetWaitReady(ctx context.Context) error
}

var _ netReadier = (*agent.Agent)(nil)

// Kipler ve (kullanilamama) nedenleri. Neden kodlarini arayuz cevirir: ns.reason.<kod>.
const (
	netModeEmbedded = "embedded"
	netModeService  = "service"
	netModeNone     = "none"

	nsReasonConnect   = "connect"   // ajan yok / bagli degil
	nsReasonPreparing = "preparing" // bagli, Zorven Net dugumu henuz hazir degil
	nsReasonNoNet     = "nonet"     // bu oturumda Zorven Net etkin degil
	nsReasonDenied    = "denied"    // CLI ajani/servis var ama bu kullanici erisemiyor
	nsReasonOld       = "old"       // calisan istemci eski surum
	nsReasonError     = "error"     // baska hata (Detail)

	// netPrepareGrace: baglanti kurulduktan sonra Zorven Net dugumunun hazir olmasi
	// icin taninan sure; sonrasinda "etkin degil" denir.
	netPrepareGrace = 25 * time.Second
	// netOpTimeout: paylasim/erisim isleminin azami suresi (CLI opTimeout ile ayni).
	netOpTimeout = 25 * time.Second
	// netReapplyTimeout: yeniden baglanma sonrasi gecici kayitlari kurma suresi.
	netReapplyTimeout = 60 * time.Second
)

// Test icin degistirilebilir kancalar (CLI'deki findAgent/callAgent gibi).
var (
	// findCtlAgent, calisan CLI ajanini/servisi bulur.
	findCtlAgent = localctl.Find
	// netBackendHook doluysa arka ucu o secer (gomulu ajan gerektirmeyen testler icin).
	netBackendHook func(a *App) (*netBackend, string, string)
	// netReadyPoll, yeniden kurarken Zorven Net dugumunun hazir olmasini yoklama araligi.
	netReadyPoll = 300 * time.Millisecond
	// netBindRetry, yerel erisim dinleyicisi yeniden acilirken denemeler arasi bekleme.
	netBindRetry = 400 * time.Millisecond
)

// --- arayuz tipleri ------------------------------------------------------------

// NetShareRow, bu cihazin sundugu bir paylasim.
type NetShareRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Proto       string   `json:"proto"`
	To          []string `json:"to,omitempty"` // "tur:kimlik"; yalniz bu ajanin kurdugu paylasimlarda bilinir
	Peers       int      `json:"peers"`        // baglanmaya yetkili es sayisi
	ExpiresUnix int64    `json:"expires_unix,omitempty"`
	// Life: persistent | temporary | "" (bilinmiyor: baska yerde kurulmus).
	Life string `json:"life,omitempty"`
	Path string `json:"path,omitempty"` // direct | relay | mixed
}

// NetAccessRow, bu cihazin erisebildigi bir paylasim.
type NetAccessRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Owner       string   `json:"owner"`
	Port        int      `json:"port"`
	Proto       string   `json:"proto"`
	ExpiresUnix int64    `json:"expires_unix,omitempty"`
	Online      bool     `json:"online"`
	Path        string   `json:"path,omitempty"` // direct | relay
	RTTMs       int      `json:"rtt_ms,omitempty"`
	Bound       []string `json:"bound,omitempty"` // bu paylasima acik yerel adresler
}

// NetBindRow, acik bir yerel erisim (paylasim -> 127.0.0.1:port).
type NetBindRow struct {
	Target string `json:"target"`
	Local  string `json:"local"`
	Error  string `json:"error,omitempty"` // yeniden kurulamadiysa son hata
}

// NetShareView, sayfanin tum durumu (arayuz periyodik yoklar).
type NetShareView struct {
	// OK: Zorven Net kullanilabilir. Degilse Reason/Detail nedenini soyler.
	OK     bool   `json:"ok"`
	Mode   string `json:"mode"` // embedded | service | none
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	// CanList: erisilebilir paylasim listesi sunuluyor (servis kipinde yok).
	CanList bool `json:"can_list"`
	// PersistForced: paylasimlar her zaman kalici (sistem servisi).
	PersistForced bool `json:"persist_forced"`
	// NeedElevation/CanElevate: servis var ama bu kullanici erisemiyor; Windows'ta
	// uygulama yonetici olarak yeniden baslatilabilir.
	NeedElevation bool           `json:"need_elevation"`
	CanElevate    bool           `json:"can_elevate"`
	Shares        []NetShareRow  `json:"shares"`
	Access        []NetAccessRow `json:"access"`
	Binds         []NetBindRow   `json:"binds"`
	// Devices: aga kayitli cihaz adlari (alici onerisi: device:<ad>).
	Devices []string `json:"devices"`
	// Warn: yeniden baglanma sonrasi kurulamayan gecici paylasimlar.
	Warn []string `json:"warn,omitempty"`
}

// NetShareRequest, yeni paylasim istegi (arayuz formu).
type NetShareRequest struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto"`   // tcp | udp (bos = tcp)
	Name    string `json:"name"`    // bos = <cihaz>-<port>
	To      string `json:"to"`      // virgul/bosluk ayirmali alicilar
	Expires string `json:"expires"` // 24h gibi sure; bos = suresiz
	Persist bool   `json:"persist"` // ajan bitince silme
}

// NetShareResult, paylasim/erisim eyleminin sonucu.
type NetShareResult struct {
	OK    bool         `json:"ok"`
	Error string       `json:"error,omitempty"`
	Share *NetShareRow `json:"share,omitempty"`
	Local string       `json:"local,omitempty"` // erisim: gercek dinleme adresi
}

// --- CLI ile ayni ayrisma / dogrulama ------------------------------------------

// parseGrantee: client/net_cmds.go'daki ile ayni kurallar.
// "user:ali@x.com" | "group:role:admin" | "device:nas" | "everyone" | "role:admin" |
// "ali@x.com" | "nas".
func parseGrantee(s string) localctl.Grantee {
	s = strings.TrimSpace(s)
	for _, p := range []string{"user", "group", "device"} {
		if v, ok := strings.CutPrefix(s, p+":"); ok {
			return localctl.Grantee{Type: p, ID: v}
		}
	}
	switch {
	case s == "everyone" || strings.HasPrefix(s, "role:"):
		return localctl.Grantee{Type: "group", ID: s}
	case strings.Contains(s, "@"):
		return localctl.Grantee{Type: "user", ID: s}
	}
	return localctl.Grantee{Type: "device", ID: s}
}

// parseGrantees, form alanindaki alicilari ayirir (virgul, noktalivirgul ya da
// bosluk). CLI'de her --to ayri bayraktir; e-posta ve kimliklerde bosluk/virgul olmaz.
func parseGrantees(s string) []localctl.Grantee {
	var out []localctl.Grantee
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
		out = append(out, parseGrantee(f))
	}
	return out
}

// buildShareSpec, formu CLI'deki `zorven share` ile ayni dogrulamadan gecirip
// ajana gidecek istege cevirir.
func buildShareSpec(req NetShareRequest) (localctl.ShareSpec, error) {
	if req.Port < 1 || req.Port > 65535 {
		return localctl.ShareSpec{}, errors.New(tm("app.badPort"))
	}
	proto := strings.ToLower(strings.TrimSpace(req.Proto))
	switch proto {
	case "":
		proto = "tcp"
	case "tcp", "udp":
	default:
		return localctl.ShareSpec{}, errors.New(tm("ns.err.proto", req.Proto))
	}
	to := parseGrantees(req.To)
	if len(to) == 0 {
		return localctl.ShareSpec{}, errors.New(tm("ns.err.to"))
	}
	sp := localctl.ShareSpec{Name: strings.TrimSpace(req.Name), Port: req.Port, Proto: proto, To: to, Persist: req.Persist}
	if exp := strings.TrimSpace(req.Expires); exp != "" {
		d, err := time.ParseDuration(exp)
		if err != nil || d < time.Minute {
			return localctl.ShareSpec{}, errors.New(tm("ns.err.expires", exp))
		}
		sp.ExpiresSec = int(d.Seconds())
	}
	return sp, nil
}

// accessLocalAddr, erisimin yerel adresini CLI ile ayni kuralla belirler: yerel
// port verildiyse o; verilmediyse hedefteki ":port"; o da yoksa bos ("127.0.0.1:"),
// yani ajan paylasimin kendi portunu kullanir. Yalniz 127.0.0.1 (loopback).
func accessLocalAddr(target string, local int) (string, error) {
	const bind = "127.0.0.1"
	if local < 0 || local > 65535 {
		return "", errors.New(tm("ns.err.local"))
	}
	if local > 0 {
		return net.JoinHostPort(bind, strconv.Itoa(local)), nil
	}
	if _, p, ok := strings.Cut(target, ":"); ok {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return net.JoinHostPort(bind, strconv.Itoa(n)), nil
		}
	}
	return net.JoinHostPort(bind, ""), nil
}

// --- IPC arka ucu (CLI ajani / servis) -----------------------------------------

// ipcNet, ayni makinedeki CLI ajaninin/servisin Zorven Net yuzeyi; CLI'deki
// share/access komutlarinin kullandigi localctl islemleriyle konusur.
type ipcNet struct{ ep localctl.Endpoint }

var _ localctl.NetController = ipcNet{}

func (n ipcNet) ShareCreate(_ context.Context, sp localctl.ShareSpec) (localctl.ShareInfo, error) {
	resp, err := localctl.Call(n.ep, localctl.Request{Op: "share_create", Share: &sp})
	if err != nil {
		return localctl.ShareInfo{}, err
	}
	if len(resp.Shares) == 0 {
		return localctl.ShareInfo{}, errors.New(tm("ns.err.noinfo"))
	}
	return resp.Shares[0], nil
}

func (n ipcNet) ShareDelete(_ context.Context, idOrName string) (localctl.ShareInfo, error) {
	resp, err := localctl.Call(n.ep, localctl.Request{Op: "share_delete", ShareID: idOrName})
	if err != nil {
		return localctl.ShareInfo{}, err
	}
	if len(resp.Shares) > 0 {
		return resp.Shares[0], nil
	}
	return localctl.ShareInfo{ID: idOrName, Name: idOrName}, nil
}

func (n ipcNet) ShareList() ([]localctl.ShareInfo, error) {
	resp, err := localctl.Call(n.ep, localctl.Request{Op: "share_list"})
	if err != nil {
		return nil, err
	}
	return resp.Shares, nil
}

func (n ipcNet) AccessOpen(_ context.Context, target, local string) (string, error) {
	resp, err := localctl.Call(n.ep, localctl.Request{Op: "access_open", Target: target, Local: local})
	if err != nil {
		return "", err
	}
	return resp.Local, nil
}

func (n ipcNet) AccessClose(local string) error {
	_, err := localctl.Call(n.ep, localctl.Request{Op: "access_close", Local: local})
	return err
}

func (n ipcNet) paths() ([]localctl.PathInfo, error) {
	resp, err := localctl.Call(n.ep, localctl.Request{Op: "net_paths"})
	if err != nil {
		return nil, err
	}
	return resp.Paths, nil
}

// --- arka uc secimi ------------------------------------------------------------

// netBackend, paylasim/erisim islemlerinin yurutuldugu ajan.
type netBackend struct {
	ctl     localctl.NetController
	mode    string
	service bool // sistem servisi: paylasimlar her zaman kalici
	// listIn: erisilebilir paylasimlar (nil: bu kipte sunulmuyor).
	listIn func() ([]protocol.NetShareIn, error)
	// paths: esler ve secili yol (nil: sunulmuyor).
	paths func() ([]localctl.PathInfo, error)
}

// netBackendNow, simdi kullanilacak ajani secer. Gomulu ajan canliysa (baglaniyor/
// bagli/yeniden baglaniyor) o; degilse ayni makinedeki CLI ajani/servis (IPC).
// Hicbiri yoksa nil ve neden kodu doner.
func (a *App) netBackendNow() (be *netBackend, reason, detail string) {
	if netBackendHook != nil {
		return netBackendHook(a)
	}
	a.mu.Lock()
	ag, st := a.ag, a.status
	a.mu.Unlock()
	if ag != nil && isLiveState(st.State) {
		return &netBackend{ctl: ag, mode: netModeEmbedded, listIn: ag.NetSharesIn, paths: ag.NetPaths}, "", ""
	}
	ep, _, err := findCtlAgent()
	if ep != nil {
		ipc := ipcNet{ep: *ep}
		return &netBackend{ctl: ipc, mode: netModeService, service: ep.Service, paths: ipc.paths}, "", ""
	}
	if err != nil {
		// Ajan var ama konusamiyoruz (izin vb.): CLI'deki share.warn.unreach durumu.
		if isPermissionErr(err) {
			return nil, nsReasonDenied, err.Error()
		}
		return nil, nsReasonError, err.Error()
	}
	return nil, nsReasonConnect, ""
}

func isLiveState(s agent.State) bool {
	return s == agent.StateConnected || s == agent.StateConnecting || s == agent.StateReconnecting
}

// isPermissionErr, yerel kontrol kanalina erisim izni hatasini tanir (nettun.go ile ayni olcut).
func isPermissionErr(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return errors.Is(err, os.ErrPermission) || strings.Contains(m, "access is denied") || strings.Contains(m, "permission denied") || strings.Contains(m, "yetkisiz")
}

// netReasonFor, ShareList hatasindan kullanilamama nedenini cikarir.
func (a *App) netReasonFor(be *netBackend, err error) (reason, detail string) {
	switch {
	case localctl.IsUnsupported(err):
		return nsReasonOld, ""
	case isPermissionErr(err):
		return nsReasonDenied, err.Error()
	case errors.Is(err, localctl.ErrNoNet) || strings.Contains(err.Error(), localctl.ErrNoNet.Error()):
		if be.mode == netModeEmbedded {
			a.mu.Lock()
			since := a.connAt
			a.mu.Unlock()
			if since.IsZero() || time.Since(since) < netPrepareGrace {
				return nsReasonPreparing, ""
			}
		}
		return nsReasonNoNet, ""
	}
	return nsReasonError, err.Error()
}

// netErrText, eylem hatasini kullaniciya gosterilecek metne cevirir (CLI'deki mapAgentErr).
func netErrText(err error) string {
	switch {
	case localctl.IsUnsupported(err):
		return tm("ns.err.old")
	case strings.Contains(err.Error(), localctl.ErrNoNet.Error()):
		return tm("ns.err.nonet")
	case strings.Contains(err.Error(), "yetkisiz"):
		return tm("ns.err.denied", err.Error())
	}
	return err.Error()
}

// netReasonText, arka uc secilemediginde eylem icin gosterilecek metin.
func netReasonText(reason, detail string) string {
	switch reason {
	case nsReasonConnect:
		return tm("ns.err.connect")
	case nsReasonDenied:
		return tm("ns.err.denied", detail)
	}
	return tm("ns.err.unreach", detail)
}

// --- bu oturumda kurulanlarin kaydi --------------------------------------------

// wantedShare, bu uygulamanin kurdugu gecici (kalici olmayan) paylasim. Sunucu
// gecici paylasimlari cihaz oturumuyla birlikte siler; yeniden baglaninca
// (CLI'deki `zorven share` NetShares gibi) yeniden kurulur.
type wantedShare struct {
	spec      localctl.ShareSpec
	expiresAt time.Time // sifir: suresiz
	seen      bool      // seenEpoch baglantisinda listede goruldu
	seenEpoch uint64
	err       string // son yeniden kurma hatasi
}

// netBind, acik bir yerel erisim. Dinleyici ajanla birlikte kapandigi icin gomulu
// ajan yeniden baglaninca ayni hedef ve adresle yeniden acilir. mode, erisimin hangi
// arka ucta (embedded | service) acildigini soyler: servis kipindekiler masaustunce
// yeniden kurulamaz ve yalniz o kipteyken listelenir.
type netBind struct {
	target string
	local  string
	mode   string
	err    string
}

// netTrack, gecici paylasim ve yerel erisim kayitlari (yalniz bellekte; Baglantiyi
// kes / cikis ve uygulamadan cikis bunlari siler, tipki CLI'de Ctrl+C gibi).
type netTrack struct {
	mu     sync.Mutex
	epoch  uint64 // her (yeniden) baglanmada artar
	shares []*wantedShare
	binds  []*netBind

	reapplyMu sync.Mutex // reapply'i siraya dizer
}

func normProto(p string) string {
	if p == "" {
		return "tcp"
	}
	return p
}

func (t *netTrack) addShare(sp localctl.ShareSpec, expiresAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, w := range t.shares {
		if w.spec.Port == sp.Port && normProto(w.spec.Proto) == normProto(sp.Proto) {
			t.shares[i] = &wantedShare{spec: sp, expiresAt: expiresAt}
			return
		}
	}
	t.shares = append(t.shares, &wantedShare{spec: sp, expiresAt: expiresAt})
}

func (t *netTrack) removeShare(port int, proto string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.shares[:0]
	for _, w := range t.shares {
		if !(w.spec.Port == port && normProto(w.spec.Proto) == normProto(proto)) {
			out = append(out, w)
		}
	}
	t.shares = out
}

// observe, ajanin listesine bakip kayitlari gunceller: bir paylasim bu baglantida
// listede goruldukten sonra kaybolduysa baska yerden silinmis ya da suresi dolmustur;
// yeniden baglaninca geri kurulmasin diye kayittan dusurulur. Yeniden baglanma
// aninda (epoch degisti) listede olmamasi silinmis sayilmaz.
func (t *netTrack) observe(list []localctl.ShareInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.shares[:0]
	for _, w := range t.shares {
		present := false
		for _, si := range list {
			if si.Port == w.spec.Port && normProto(si.Proto) == normProto(w.spec.Proto) {
				present = true
				break
			}
		}
		switch {
		case present:
			w.seen, w.seenEpoch, w.err = true, t.epoch, ""
		case w.seen && w.seenEpoch == t.epoch:
			continue // goruldu, sonra kayboldu: dusur
		}
		out = append(out, w)
	}
	t.shares = out
}

// bumpEpoch, yeni bir baglanti kuruldugunu isaretler (publish, baglanti duyurulur
// duyurulmaz ve Zorven Net dugumu kaydedilmeden ONCE senkron cagirir): bundan sonra
// onceki baglantida gorulen paylasimlarin listede olmamasi "silindi" sayilmaz.
func (t *netTrack) bumpEpoch() {
	t.mu.Lock()
	t.epoch++
	t.mu.Unlock()
}

func (t *netTrack) addBind(target, local, mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range t.binds {
		if b.local == local {
			b.target, b.mode, b.err = target, mode, ""
			return
		}
	}
	t.binds = append(t.binds, &netBind{target: target, local: local, mode: mode})
}

func (t *netTrack) removeBind(local string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.binds[:0]
	for _, b := range t.binds {
		if b.local != local {
			out = append(out, b)
		}
	}
	t.binds = out
}

func (t *netTrack) hasBind(local string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range t.binds {
		if b.local == local {
			return true
		}
	}
	return false
}

func (t *netTrack) setBindErr(local, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range t.binds {
		if b.local == local {
			b.err = msg
		}
	}
}

// bindRows, mode arka ucunda acilmis erisimleri doner.
func (t *netTrack) bindRows(mode string) []netBind {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]netBind, 0, len(t.binds))
	for _, b := range t.binds {
		if b.mode == mode {
			out = append(out, *b)
		}
	}
	return out
}

func (t *netTrack) warnings() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for _, w := range t.shares {
		if w.err != "" {
			out = append(out, w.spec.Name+" ("+strconv.Itoa(w.spec.Port)+"/"+normProto(w.spec.Proto)+"): "+w.err)
		}
	}
	return out
}

func (t *netTrack) clear() {
	t.mu.Lock()
	t.shares, t.binds = nil, nil
	t.mu.Unlock()
}

// reapply, ajan (yeniden) baglandiginda kayitli gecici paylasimlari ve yerel
// erisimleri ctl uzerinde yeniden kurar. Zorven Net dugumu hazir olana kadar
// bekler; sunucuda zaten duran paylasim yeniden olusturulmaz (yeni baglanti
// eskinin yerini aldiysa gecici paylasim silinmemis olabilir). alive false
// donerse (ajan degisti ya da baglanti kesildi) birakir. Epoch'u publish
// (bumpEpoch) artirmistir.
func (t *netTrack) reapply(ctx context.Context, ctl localctl.NetController, alive func() bool, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	t.reapplyMu.Lock()
	defer t.reapplyMu.Unlock()

	t.mu.Lock()
	shares := make([]wantedShare, 0, len(t.shares))
	for _, w := range t.shares {
		shares = append(shares, *w)
	}
	binds := make([]netBind, 0, len(t.binds))
	for _, b := range t.binds {
		if b.mode == netModeEmbedded { // servis kipindekiler gomulu ajanda yeniden kurulmaz
			binds = append(binds, *b)
		}
	}
	t.mu.Unlock()
	if len(shares) == 0 && len(binds) == 0 {
		return
	}

	var list []localctl.ShareInfo
	for {
		l, err := ctl.ShareList()
		if err == nil {
			list = l
			break
		}
		if !errors.Is(err, localctl.ErrNoNet) {
			log.Warn("zorven net: paylasimlar okunamadi", "hata", err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(netReadyPoll):
		}
		if !alive() {
			return
		}
	}
	// Dugum, ilk netmap'ten ONCE kayit edilir: o ana kadarki liste bos olabilir ve
	// sunucuda kalmis (yeni baglanti eskinin yerini aldi) bir gecici paylasimi
	// "yok" sanip cakisma yaratmamak icin netmap'i bekleyip listeyi yeniden oku.
	if r, ok := ctl.(netReadier); ok {
		if err := r.NetWaitReady(ctx); err != nil {
			log.Warn("zorven net: dugum hazir olmadi", "hata", err)
			return
		}
		if l, err := ctl.ShareList(); err == nil {
			list = l
		}
	}

	for _, w := range shares {
		if !alive() {
			return
		}
		present := false
		for _, si := range list {
			if si.Port == w.spec.Port && normProto(si.Proto) == normProto(w.spec.Proto) {
				present = true
				break
			}
		}
		if present {
			continue
		}
		sp := w.spec
		if !w.expiresAt.IsZero() {
			rem := time.Until(w.expiresAt)
			if rem <= 0 {
				t.removeShare(sp.Port, sp.Proto) // suresi dolmus
				continue
			}
			sp.ExpiresSec = int(rem.Seconds())
			if sp.ExpiresSec < 1 {
				sp.ExpiresSec = 1
			}
		}
		if _, err := ctl.ShareCreate(ctx, sp); err != nil {
			log.Warn("zorven net: gecici paylasim yeniden kurulamadi", "ad", sp.Name, "port", sp.Port, "hata", err)
			t.mu.Lock()
			for _, x := range t.shares {
				if x.spec.Port == sp.Port && normProto(x.spec.Proto) == normProto(sp.Proto) {
					x.err = netErrText(err)
				}
			}
			t.mu.Unlock()
		}
	}

	for _, b := range binds {
		if !alive() {
			return
		}
		var err error
		// Eski dinleyici henuz birakilmamis olabilir: kisa aralikla birkac deneme.
		for try := 0; try < 5; try++ {
			if _, err = ctl.AccessOpen(ctx, b.target, b.local); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(netBindRetry):
			}
			if !alive() {
				return
			}
		}
		if err != nil {
			log.Warn("zorven net: yerel erisim yeniden acilamadi", "hedef", b.target, "adres", b.local, "hata", err)
			t.setBindErr(b.local, netErrText(err))
		} else {
			t.setBindErr(b.local, "")
		}
	}
}

// netReapply, gomulu ajan (yeniden) baglaninca cagrilir (publish).
func (a *App) netReapply() {
	a.mu.Lock()
	ag := a.ag
	a.mu.Unlock()
	if ag == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), netReapplyTimeout)
	defer cancel()
	a.nt.reapply(ctx, ag, func() bool { return a.isCurrent(ag) }, a.log)
}

// --- arayuze acilan metotlar ---------------------------------------------------

// NetShareState, Paylasim sayfasinin durumunu doner: paylasimlarim, erisebildiklerim
// (gomulu ajanda), acik yerel erisimler ve kullanilamama nedeni.
func (a *App) NetShareState() NetShareView {
	v := NetShareView{
		Mode: netModeNone, Shares: []NetShareRow{}, Access: []NetAccessRow{}, Binds: []NetBindRow{}, Devices: []string{},
	}
	be, reason, detail := a.netBackendNow()
	if be == nil {
		v.Reason, v.Detail = reason, detail
		if reason == nsReasonDenied {
			v.NeedElevation, v.CanElevate = true, runtime.GOOS == "windows"
		}
		return v
	}
	v.Mode, v.CanList, v.PersistForced = be.mode, be.listIn != nil, be.service
	binds := a.nt.bindRows(be.mode)
	for _, b := range binds {
		v.Binds = append(v.Binds, NetBindRow{Target: b.target, Local: b.local, Error: b.err})
	}

	list, err := be.ctl.ShareList()
	if err != nil {
		v.Reason, v.Detail = a.netReasonFor(be, err)
		if v.Reason == nsReasonDenied {
			v.NeedElevation, v.CanElevate = true, runtime.GOOS == "windows"
		}
		return v
	}
	v.OK = true
	if be.mode == netModeEmbedded {
		a.nt.observe(list)
		v.Warn = a.nt.warnings()
	}
	for _, si := range list {
		v.Shares = append(v.Shares, shareRow(si))
	}

	byIP := map[string]localctl.PathInfo{}
	if be.paths != nil {
		if ps, perr := be.paths(); perr == nil {
			for _, p := range ps {
				byIP[p.IP] = p
				v.Devices = append(v.Devices, p.Name)
			}
		}
	}
	if be.listIn != nil {
		if in, ierr := be.listIn(); ierr == nil {
			v.Access = accessRows(in, byIP, binds)
		}
	}
	return v
}

func shareRow(si localctl.ShareInfo) NetShareRow {
	r := NetShareRow{
		ID: si.ID, Name: si.Name, Port: si.Port, Proto: si.Proto, Peers: len(si.Allow),
		ExpiresUnix: si.ExpiresUnix, Path: si.Path,
	}
	for _, g := range si.To {
		r.To = append(r.To, g.Type+":"+g.ID)
	}
	switch {
	case si.Persistent:
		r.Life = "persistent"
	case len(si.To) > 0:
		r.Life = "temporary" // yalniz bu ajan kurdugunda bilinir
	}
	return r
}

// targetMatches, erisim hedefi ("ad" ya da "ad:port") paylasimi secer mi
// (CLI'deki cozumle ayni: wgnet.ResolveShare).
func targetMatches(target string, s protocol.NetShareIn) bool {
	st := protocol.NewNetState()
	st.SharesIn[s.ID] = s
	_, err := wgnet.ResolveShare(st, target)
	return err == nil
}

func accessRows(in []protocol.NetShareIn, byIP map[string]localctl.PathInfo, binds []netBind) []NetAccessRow {
	out := make([]NetAccessRow, 0, len(in))
	for _, s := range in {
		r := NetAccessRow{ID: s.ID, Name: s.Name, Owner: s.OwnerName, Port: s.Port, Proto: s.Proto, ExpiresUnix: s.ExpiresUnix}
		if p, ok := byIP[s.OwnerIP]; ok {
			r.Online, r.Path, r.RTTMs = p.Online, p.Path, p.RTTMs
		}
		for _, b := range binds {
			if b.err == "" && targetMatches(b.target, s) {
				r.Bound = append(r.Bound, b.local)
			}
		}
		out = append(out, r)
	}
	return out
}

// NetShareCreate, bu cihazin bir portunu secili alicilarla paylasir (`zorven share`).
func (a *App) NetShareCreate(req NetShareRequest) NetShareResult {
	sp, err := buildShareSpec(req)
	if err != nil {
		return NetShareResult{Error: err.Error()}
	}
	be, reason, detail := a.netBackendNow()
	if be == nil {
		return NetShareResult{Error: netReasonText(reason, detail)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	si, err := be.ctl.ShareCreate(ctx, sp)
	if err != nil {
		return NetShareResult{Error: netErrText(err)}
	}
	// Gecici paylasim ajan oturumuyla biter: gomulu ajanda yeniden baglaninca
	// yeniden kurulabilsin diye kaydedilir (kalici ve servis paylasimlari sunucuda kalir).
	if be.mode == netModeEmbedded && !sp.Persist {
		keep := sp
		if si.Name != "" {
			keep.Name = si.Name
		}
		var exp time.Time
		if si.ExpiresUnix > 0 {
			exp = time.Unix(si.ExpiresUnix, 0)
		}
		a.nt.addShare(keep, exp)
	}
	row := shareRow(si)
	return NetShareResult{OK: true, Share: &row}
}

// NetShareDelete, bu cihazin bir paylasimini id ya da adla siler (`zorven share rm`).
func (a *App) NetShareDelete(idOrName string) NetShareResult {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return NetShareResult{Error: tm("ns.err.share")}
	}
	be, reason, detail := a.netBackendNow()
	if be == nil {
		return NetShareResult{Error: netReasonText(reason, detail)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	si, err := be.ctl.ShareDelete(ctx, idOrName)
	if err != nil {
		return NetShareResult{Error: netErrText(err)}
	}
	a.nt.removeShare(si.Port, si.Proto)
	row := shareRow(si)
	return NetShareResult{OK: true, Share: &row}
}

// NetAccessOpen, size acilan bir paylasima yerel 127.0.0.1 portundan baglanir
// (`zorven access <ad[:port]> --local <port>`). localPort 0 ise paylasimin portu.
func (a *App) NetAccessOpen(target string, localPort int) NetShareResult {
	target = strings.TrimSpace(target)
	if target == "" {
		return NetShareResult{Error: tm("ns.err.target")}
	}
	local, err := accessLocalAddr(target, localPort)
	if err != nil {
		return NetShareResult{Error: err.Error()}
	}
	be, reason, detail := a.netBackendNow()
	if be == nil {
		return NetShareResult{Error: netReasonText(reason, detail)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	addr, err := be.ctl.AccessOpen(ctx, target, local)
	if err != nil {
		return NetShareResult{Error: netErrText(err)}
	}
	if addr == "" {
		addr = local
	}
	a.nt.addBind(target, addr, be.mode)
	return NetShareResult{OK: true, Local: addr}
}

// NetAccessClose, NetAccessOpen ile acilan yerel erisimi kapatir.
func (a *App) NetAccessClose(local string) NetShareResult {
	local = strings.TrimSpace(local)
	if local == "" {
		return NetShareResult{Error: tm("ns.err.local")}
	}
	be, reason, detail := a.netBackendNow()
	if be == nil {
		if reason == nsReasonConnect {
			// Ajan yoksa dinleyici de yoktur; kaydi birak.
			a.nt.removeBind(local)
			return NetShareResult{OK: true}
		}
		return NetShareResult{Error: netReasonText(reason, detail)}
	}
	err := be.ctl.AccessClose(local)
	if err != nil {
		// Dinleyici zaten kapanmis (ajan yeniden baglandi vb.): kaydi dusur.
		if strings.Contains(err.Error(), "dinleyicisi yok") && a.nt.hasBind(local) {
			a.nt.removeBind(local)
			return NetShareResult{OK: true}
		}
		return NetShareResult{Error: netErrText(err)}
	}
	a.nt.removeBind(local)
	return NetShareResult{OK: true}
}
