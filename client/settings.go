package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/internal/tui"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// zorven settings: Claude CLI tarzi, ok tuslari + Enter + Esc ile gezilen
// satir ici ayar arayuzu. Her degisiklik aninda ayar dosyasina yazilir.
//
// Model (settingsModel) terminalden bagimsizdir: tuslari isler ve cizilecek
// satirlari uretir; boylece test edilebilir. Terminal katmani internal/tui'de.

type setItem int

const (
	siScope setItem = iota
	siLanguage
	siServer
	siToken
	siInsecure
	siCACert
	siTerminal
	siScreen
	siRunning
	siUpdate
	siQuit
	siCount
)

type settingsModel struct {
	cursor  setItem
	system  bool // kapsam: false = kullanici, true = sistem (servis)
	path    string
	cfg     config.Config
	loadErr string

	editing bool
	buf     []rune

	msg    string
	msgErr bool

	running string // calisan istemci ozeti (localctl)

	// Guncelleme arayuz icinde, arka planda calisir: startUpdate dongu
	// tarafindan alinip goroutine baslatilir; sonuc updateDone ile gelir.
	updating    bool
	startUpdate bool

	version string
	done    bool

	// Testler icin degistirilebilir.
	userPath   func() (string, error)
	systemPath func() string
	probe      func() string
}

func newSettingsModel(version string) *settingsModel {
	m := &settingsModel{
		version:    version,
		userPath:   config.Path,
		systemPath: config.SystemPath,
		probe:      probeRunningClient,
	}
	m.init()
	return m
}

// init, hangi dosyanin etkin oldugunu config.Load ile ayni kuralla secer:
// kullanici dosyasinda token yoksa ve sistem dosyasinda varsa sistem kapsami.
func (m *settingsModel) init() {
	up, err := m.userPath()
	if err == nil {
		if c, lerr := config.LoadFile(up); lerr == nil && c.Token == "" {
			if s, serr := config.LoadFile(m.systemPath()); serr == nil && s.Token != "" {
				m.system = true
			}
		}
	}
	m.load()
	m.running = m.probe()
}

func (m *settingsModel) load() {
	m.loadErr = ""
	if m.system {
		m.path = m.systemPath()
	} else {
		p, err := m.userPath()
		if err != nil {
			m.loadErr = err.Error()
		}
		m.path = p
	}
	cfg, err := config.LoadFile(m.path)
	if err != nil {
		m.loadErr = err.Error()
	}
	m.cfg = cfg
}

func (m *settingsModel) save(okMsg string) {
	if m.loadErr != "" {
		m.setErr(i18n.T("set.err.readfail", m.loadErr))
		m.load()
		return
	}
	if err := config.SaveFile(m.path, m.cfg); err != nil {
		hint := ""
		if m.system && (errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "denied")) {
			hint = i18n.T("set.err.admin")
		}
		m.setErr(i18n.T("set.err.save", err.Error()) + hint)
		m.load()
		return
	}
	if m.running != "" && m.running != i18n.T("set.notrunning") && m.cursor != siLanguage {
		okMsg += i18n.T("set.saved.suffix")
	}
	m.setOK(okMsg)
}

func (m *settingsModel) setOK(s string)  { m.msg, m.msgErr = s, false }
func (m *settingsModel) setErr(s string) { m.msg, m.msgErr = s, true }

// handle, tek bir tus olayini isler.
func (m *settingsModel) handle(k tui.Key) {
	if k.Kind == tui.KeyCtrlC {
		m.done = true
		return
	}
	if m.editing {
		m.handleEdit(k)
		return
	}
	switch k.Kind {
	case tui.KeyUp:
		m.move(-1)
	case tui.KeyDown, tui.KeyTab:
		m.move(1)
	case tui.KeyHome:
		m.cursor = 0
	case tui.KeyEnd:
		m.cursor = siCount - 1
	case tui.KeyEsc:
		m.done = true
	case tui.KeyEnter:
		m.activate(1)
	case tui.KeyLeft, tui.KeyRight:
		if m.isToggle(m.cursor) {
			d := 1
			if k.Kind == tui.KeyLeft {
				d = -1
			}
			m.activate(d)
		}
	case tui.KeyRune:
		switch k.Text {
		case "k":
			m.move(-1)
		case "j":
			m.move(1)
		case "q":
			m.done = true
		case " ":
			if m.isToggle(m.cursor) {
				m.activate(1)
			}
		case "r":
			m.running = m.probe()
			m.setOK(i18n.T("set.refreshed"))
		}
	}
}

func (m *settingsModel) isToggle(i setItem) bool {
	return i == siScope || i == siLanguage || i == siInsecure || i == siTerminal || i == siScreen
}

func (m *settingsModel) move(d int) {
	n := int(siCount)
	m.cursor = setItem((int(m.cursor) + d + n) % n)
}

// activate, secili satiri calistirir; d secenekli satirlarda (dil) yon.
func (m *settingsModel) activate(d int) {
	m.msg = ""
	switch m.cursor {
	case siScope:
		m.system = !m.system
		m.load()
		if m.loadErr != "" {
			m.setErr(i18n.T("set.err.read", m.loadErr))
		} else {
			m.applyLanguage()
			m.setOK(i18n.T("set.ok.scope", m.scopeLabel()))
		}
	case siLanguage:
		m.cfg.Language = nextLanguage(m.currentLanguage(), d)
		i18n.Set(m.cfg.Language)
		m.running = m.probe() // ozet metni yeni dilde
		m.save(i18n.T("set.ok.language", i18n.Name(m.cfg.Language)))
	case siInsecure:
		m.cfg.Insecure = !m.cfg.Insecure
		m.save(i18n.T("set.ok.insecure", onOffText(m.cfg.Insecure)))
	case siTerminal:
		m.cfg.EnableTerminal = !m.cfg.EnableTerminal
		m.save(i18n.T("set.ok.terminal", onOffText(m.cfg.EnableTerminal)))
	case siScreen:
		m.cfg.EnableScreen = !m.cfg.EnableScreen
		m.save(i18n.T("set.ok.screen", onOffText(m.cfg.EnableScreen)))
	case siServer, siCACert:
		m.editing = true
		if m.cursor == siServer {
			m.buf = []rune(m.cfg.ServerAddr)
		} else {
			m.buf = []rune(m.cfg.CACertPath)
		}
	case siToken:
		// Token asla geri gosterilmez: bos baslar, yenisi yapistirilir.
		m.editing = true
		m.buf = nil
	case siRunning:
		m.running = m.probe()
		m.setOK(i18n.T("set.refreshed"))
	case siUpdate:
		if m.updating {
			m.setOK(i18n.T("set.update.busy"))
			return
		}
		m.updating, m.startUpdate = true, true
		m.setOK(i18n.T("set.updating"))
	case siQuit:
		m.done = true
	}
}

// updateDone, arka plandaki guncellemenin sonucunu ekrana yansitir.
func (m *settingsModel) updateDone(lines []string, err error) {
	m.updating = false
	m.running = m.probe()
	if err != nil {
		m.setErr(err.Error())
		return
	}
	m.setOK(strings.Join(lines, " · "))
}

// currentLanguage, dosyada dil yoksa etkin (algilanan) dil.
func (m *settingsModel) currentLanguage() string {
	if l := i18n.Normalize(m.cfg.Language); l != "" {
		return l
	}
	return i18n.Lang()
}

// applyLanguage, kapsam degisince o dosyanin dilini etkinlestirir.
func (m *settingsModel) applyLanguage() {
	if l := i18n.Normalize(m.cfg.Language); l != "" {
		i18n.Set(l)
	}
}

func nextLanguage(cur string, d int) string {
	n := len(i18n.Supported)
	for i, l := range i18n.Supported {
		if l == cur {
			return i18n.Supported[(i+d+n)%n]
		}
	}
	return i18n.Supported[0]
}

func (m *settingsModel) handleEdit(k tui.Key) {
	switch k.Kind {
	case tui.KeyEsc:
		m.editing = false
		m.buf = nil
		m.setOK(i18n.T("set.cancelled"))
	case tui.KeyEnter:
		m.commitEdit()
	case tui.KeyBackspace:
		if len(m.buf) > 0 {
			m.buf = m.buf[:len(m.buf)-1]
		}
	case tui.KeyCtrlU:
		m.buf = nil
	case tui.KeyRune:
		m.buf = append(m.buf, []rune(k.Text)...)
	}
}

func (m *settingsModel) commitEdit() {
	v := strings.TrimSpace(string(m.buf))
	switch m.cursor {
	case siServer:
		if v == "" {
			m.setErr(i18n.T("set.err.server"))
			return
		}
		if strings.ContainsAny(v, " /") {
			m.setErr(i18n.T("set.err.serverfmt"))
			return
		}
		m.cfg.ServerAddr = v
		m.editing, m.buf = false, nil
		m.save(i18n.T("set.ok.server", v))
	case siToken:
		if v == "" {
			m.editing, m.buf = false, nil
			m.setOK(i18n.T("set.ok.tokenkeep"))
			return
		}
		if !strings.HasPrefix(v, "zrv_live_") && !strings.HasPrefix(v, "rpsh_live_") {
			m.setErr(i18n.T("set.err.token"))
			return
		}
		m.cfg.Token = v
		m.editing, m.buf = false, nil
		m.save(i18n.T("set.ok.token"))
	case siCACert:
		if v != "" {
			if st, err := os.Stat(v); err != nil || st.IsDir() {
				m.setErr(i18n.T("set.err.nofile", v))
				return
			}
		}
		m.cfg.CACertPath = v
		m.editing, m.buf = false, nil
		if v == "" {
			m.save(i18n.T("set.ok.cacertrm"))
		} else {
			m.save(i18n.T("set.ok.cacert", v))
		}
	}
}

func (m *settingsModel) scopeLabel() string {
	if m.system {
		return i18n.T("set.scope.system")
	}
	return i18n.T("set.scope.user")
}

func onOffText(b bool) string {
	if b {
		return i18n.T("set.on")
	}
	return i18n.T("set.off")
}

func maskToken(t string) string {
	if t == "" {
		return i18n.T("set.undefined")
	}
	if len(t) > 16 {
		return t[:12] + "…" + t[len(t)-4:]
	}
	return strings.Repeat("•", len(t))
}

// ANSI stilleri: renkler terminal temasina uyar (sabit RGB yok).
const (
	stReset = "\x1b[0m"
	stBold  = "\x1b[1m"
	stDim   = "\x1b[2m"
	stCyan  = "\x1b[36m"
	stGreen = "\x1b[32m"
	stRed   = "\x1b[31m"
	stInv   = "\x1b[7m"
)

func toggleText(b bool) string {
	if b {
		return stGreen + "● " + i18n.T("set.on") + stReset
	}
	return stDim + "○ " + i18n.T("set.off") + stReset
}

// languageText, uc dili yan yana gosterir; secili olan vurgulanir.
func languageText(cur string) string {
	parts := make([]string, 0, len(i18n.Supported))
	for _, l := range i18n.Supported {
		if l == cur {
			parts = append(parts, stBold+"‹ "+i18n.Name(l)+" ›"+stReset)
		} else {
			parts = append(parts, stDim+i18n.Name(l)+stReset)
		}
	}
	return strings.Join(parts, "  ")
}

// padLabel, etiketi gorunur genislige gore doldurur (Japonca karakterler iki
// sutun kaplar).
func padLabel(s string, w int) string { return tui.PadRight(s, w) }

// lines, mevcut durumu cizilecek satirlara cevirir.
func (m *settingsModel) lines() []string {
	out := []string{
		"",
		stBold + " " + i18n.T("set.title") + stReset + stDim + "  ·  " + i18n.T("set.version", m.version) + stReset,
		stDim + " " + i18n.T("set.file", m.path) + stReset,
		"",
	}
	row := func(i setItem, label, value string) {
		cur := "   "
		lab := padLabel(label, 26)
		if i == m.cursor {
			cur = stCyan + " › " + stReset
			lab = stCyan + stBold + lab + stReset
		}
		out = append(out, cur+lab+" "+value)
	}
	val := func(s, empty string) string {
		if s == "" {
			return stDim + empty + stReset
		}
		return s
	}
	editVal := func(display string) string {
		return stInv + " " + display + " " + stReset + stCyan + "▏" + stReset
	}

	row(siScope, i18n.T("set.scope"), m.scopeLabel())
	row(siLanguage, i18n.T("set.language"), languageText(m.currentLanguage()))
	if m.editing && m.cursor == siServer {
		row(siServer, i18n.T("set.server"), editVal(string(m.buf)))
	} else {
		row(siServer, i18n.T("set.server"), val(m.cfg.ServerAddr, i18n.T("set.undefined")))
	}
	if m.editing && m.cursor == siToken {
		row(siToken, i18n.T("set.token"), editVal(strings.Repeat("•", len(m.buf))))
	} else {
		row(siToken, i18n.T("set.token"), maskToken(m.cfg.Token))
	}
	row(siInsecure, i18n.T("set.insecure"), toggleText(m.cfg.Insecure))
	if m.editing && m.cursor == siCACert {
		row(siCACert, i18n.T("set.cacert"), editVal(string(m.buf)))
	} else {
		row(siCACert, i18n.T("set.cacert"), val(m.cfg.CACertPath, i18n.T("set.cacert.none")))
	}
	row(siTerminal, i18n.T("set.terminal"), toggleText(m.cfg.EnableTerminal))
	row(siScreen, i18n.T("set.screen"), toggleText(m.cfg.EnableScreen))
	out = append(out, stDim+"   ──────────────────────────────"+stReset)
	row(siRunning, i18n.T("set.running"), m.running)
	upd := ""
	if m.updating {
		upd = stDim + "…" + stReset
	}
	row(siUpdate, i18n.T("set.update"), upd)
	row(siQuit, i18n.T("set.quit"), "")
	out = append(out, "")

	if m.msg != "" {
		if m.msgErr {
			out = append(out, stRed+" ✗ "+m.msg+stReset)
		} else {
			out = append(out, stGreen+" ✓ "+stReset+m.msg)
		}
	} else {
		out = append(out, stDim+" "+m.hint()+stReset)
	}
	if m.editing {
		out = append(out, stDim+" "+i18n.T("set.help.edit")+stReset)
	} else {
		out = append(out, stDim+" "+i18n.T("set.help.nav")+stReset)
	}
	return out
}

func (m *settingsModel) hint() string {
	switch m.cursor {
	case siScope:
		return i18n.T("set.hint.scope")
	case siLanguage:
		return i18n.T("set.hint.language")
	case siServer:
		return i18n.T("set.hint.server")
	case siToken:
		return i18n.T("set.hint.token")
	case siInsecure:
		return i18n.T("set.hint.insecure")
	case siCACert:
		return i18n.T("set.hint.cacert")
	case siTerminal:
		return i18n.T("set.hint.terminal")
	case siScreen:
		return i18n.T("set.hint.screen")
	case siRunning:
		return i18n.T("set.hint.running")
	case siUpdate:
		return i18n.T("set.hint.update")
	}
	return i18n.T("set.hint.quit")
}

// probeRunningClient, yerel kontrol kanalindan calisan istemciyi bulur.
func probeRunningClient() string {
	ep, r, err := localctl.Find()
	if err != nil || ep == nil || r == nil {
		return i18n.T("set.notrunning")
	}
	s := i18n.T("set.run.fg")
	if ep.Service {
		s = i18n.T("set.run.service")
	}
	if r.Version != "" {
		s += ", " + i18n.T("set.run.version", r.Version)
	}
	if r.PID > 0 {
		s += fmt.Sprintf(", pid %d", r.PID)
	}
	return s
}

type updateResult struct {
	lines []string
	err   error
}

// runSettingsUI, arayuzu calistirir.
func runSettingsUI(ctx context.Context, version string) error {
	t, err := tui.Open()
	if err != nil {
		return err
	}
	defer t.Close()
	m := newSettingsModel(version)
	m.applyLanguage()
	results := make(chan updateResult, 1)
	keys, errs := t.Events()
	t.Draw(m.lines())
	for !m.done {
		select {
		case ks := <-keys:
			for _, k := range ks {
				m.handle(k)
				if m.done {
					break
				}
			}
		case err := <-errs:
			return err
		case r := <-results:
			m.updateDone(r.lines, r.err)
		}
		if m.startUpdate {
			m.startUpdate = false
			go func() {
				lines, err := runUpdate(ctx, true, "", false, "")
				results <- updateResult{lines, err}
			}()
		}
		t.Draw(m.lines())
	}
	return nil
}

func newSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "settings",
		Aliases: []string{"set", "ayarlar"},
		Short:   i18n.T("cmd.settings"),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runSettingsUI(cmd.Context(), version)
			if errors.Is(err, tui.ErrNotTerminal) {
				return errors.New(i18n.T("set.notty"))
			}
			return err
		},
	}
}
