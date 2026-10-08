package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	imapDelim        = '/'
	imapPollInterval = 5 * time.Second  // komutlar arasi en sik DB yoklamasi
	imapIdleInterval = 20 * time.Second // IDLE'da (diger replikalar icin) periyodik yoklama
	imapOpTimeout    = 60 * time.Second
	rawCacheEntries  = 32
	rawCacheBytes    = 16 << 20
)

// imapFolder, IMAP kutu adi ile depolama klasoru eslesmesidir.
type imapFolder struct {
	Name   string
	Folder string
	Attr   imap.MailboxAttr // special-use ("" = yok)
	User   bool             // kullanici klasoru (mail_folders)
}

var imapFolders = []imapFolder{
	{"INBOX", store.MailFolderInbox, "", false},
	{"Drafts", store.MailFolderDrafts, imap.MailboxAttrDrafts, false},
	{"Sent", store.MailFolderSent, imap.MailboxAttrSent, false},
	{"Trash", store.MailFolderTrash, imap.MailboxAttrTrash, false},
}

func lookupFolder(name string) (imapFolder, bool) {
	for _, f := range imapFolders {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return imapFolder{}, false
}

func folderByStore(folder string) imapFolder {
	for _, f := range imapFolders {
		if f.Folder == folder {
			return f
		}
	}
	return imapFolders[0]
}

// userFolderAttrs, CREATE ... USE ile izin verilen special-use nitelikleri.
var userFolderAttrs = map[string]imap.MailboxAttr{
	`\archive`: imap.MailboxAttrArchive,
	`\junk`:    imap.MailboxAttrJunk,
	`\flagged`: imap.MailboxAttrFlagged,
	`\all`:     imap.MailboxAttrAll,
}

func userImapFolder(f store.MailFolder) imapFolder {
	return imapFolder{
		Name:   f.Name,
		Folder: store.MailUserFolderKey(f.Name),
		Attr:   userFolderAttrs[strings.ToLower(f.SpecialUse)],
		User:   true,
	}
}

// resolve, IMAP kutu adini sistem veya kullanici klasorune cozer.
func (s *imapSession) resolve(ctx context.Context, name string) (imapFolder, bool, error) {
	if f, ok := lookupFolder(name); ok {
		return f, true, nil
	}
	list, err := s.srv.Store.ListMailFolders(ctx, s.acct.TenantID, s.acct.Address)
	if err != nil {
		return imapFolder{}, false, err
	}
	for _, f := range list {
		if f.Name == name {
			return userImapFolder(f), true, nil
		}
	}
	return imapFolder{}, false, nil
}

// IMAPServer, Postgres tabanli IMAP4rev1/rev2 sunucusudur.
type IMAPServer struct {
	Auth   *Authenticator
	Store  store.Store
	Hub    *Hub
	Logger *slog.Logger

	srv *imapserver.Server
}

// NewIMAPServer, sunucuyu olusturur. tlsCfg nil degilse STARTTLS (ve implicit
// TLS dinleyicileri icin yapilandirma) kullanilir; insecureAuth yalnizca testlerde
// TLS'siz girise izin verir.
func NewIMAPServer(auth *Authenticator, st store.Store, hub *Hub, tlsCfg *tls.Config, insecureAuth bool, logger *slog.Logger) *IMAPServer {
	if hub == nil {
		hub = DefaultHub
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &IMAPServer{Auth: auth, Store: st, Hub: hub, Logger: logger}
	s.srv = imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			ip := ""
			if c != nil && c.NetConn() != nil {
				ip = remoteIP(c.NetConn().RemoteAddr())
			}
			return &imapSession{srv: s, ip: ip}, nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1:        {},
			imap.CapIMAP4rev2:        {},
			imap.CapNamespace:        {},
			imap.CapUIDPlus:          {},
			imap.CapMove:             {},
			imap.CapSpecialUse:       {},
			imap.CapCreateSpecialUse: {},
			imap.CapChildren:         {},
			imap.CapListExtended:     {},
			imap.CapListStatus:       {},
			imap.CapESearch:          {},
			imap.CapLiteralPlus:      {},
			imap.CapStatusSize:       {},
			imap.CapAppendLimit:      {},
		},
		TLSConfig:    tlsCfg,
		InsecureAuth: insecureAuth,
		Logger:       slogPrinter{logger},
		DebugWriter:  imapDebugWriter(logger),
	})
	return s
}

type slogPrinter struct{ l *slog.Logger }

func (p slogPrinter) Printf(format string, args ...interface{}) {
	p.l.Debug("imap: " + strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func remoteIP(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}

// Serve, verilen dinleyicide baglantilari kabul eder (TLS sarmali cagirana ait).
func (s *IMAPServer) Serve(ln net.Listener) error { return s.srv.Serve(ln) }

// ListenAndServeTLS, implicit TLS (IMAPS) ile dinler.
func (s *IMAPServer) ListenAndServeTLS(addr string) error { return s.srv.ListenAndServeTLS(addr) }

// ListenAndServe, TLS'siz (STARTTLS ile yukseltilebilir) dinler.
func (s *IMAPServer) ListenAndServe(addr string) error { return s.srv.ListenAndServe(addr) }

// Close, sunucuyu ve tum baglantilari kapatir.
func (s *IMAPServer) Close() error { return s.srv.Close() }

// --- oturum ----------------------------------------------------------------

type imapMsg struct {
	store.MailIndexEntry
}

func (m *imapMsg) flags(folder string) []imap.Flag {
	var fl []imap.Flag
	if m.Seen {
		fl = append(fl, imap.FlagSeen)
	}
	if m.Answered {
		fl = append(fl, imap.FlagAnswered)
	}
	if m.Flagged {
		fl = append(fl, imap.FlagFlagged)
	}
	if m.Deleted {
		fl = append(fl, imap.FlagDeleted)
	}
	if folder == store.MailFolderDrafts {
		fl = append(fl, imap.FlagDraft)
	}
	return fl
}

type selectedBox struct {
	f        imapFolder
	readOnly bool
	msgs     []*imapMsg
	loaded   time.Time
	notify   <-chan struct{}
	cancel   func()
}

type imapSession struct {
	srv  *IMAPServer
	ip   string
	acct *Account
	sel  *selectedBox

	cache      map[string][]byte
	cacheOrder []string
	cacheSize  int
}

var _ imapserver.SessionIMAP4rev2 = (*imapSession)(nil)
var _ imapserver.SessionAppendLimit = (*imapSession)(nil)

func (s *imapSession) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), imapOpTimeout)
}

func (s *imapSession) log() *slog.Logger { return s.srv.Logger }

func imapNo(code imap.ResponseCode, text string) error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: text}
}

func (s *imapSession) Close() error {
	s.closeSelected()
	return nil
}

func (s *imapSession) closeSelected() {
	if s.sel != nil {
		if s.sel.cancel != nil {
			s.sel.cancel()
		}
		s.sel = nil
	}
}

func (s *imapSession) Login(username, password string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	acct, err := s.srv.Auth.Authenticate(ctx, "imap", username, password, s.ip)
	if err != nil {
		if errors.Is(err, ErrAuthRateLimited) {
			return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthenticationFailed,
				Text: "Too many failed attempts, try again later"}
		}
		return imapserver.ErrAuthFailed
	}
	s.acct = acct
	return nil
}

func (s *imapSession) AppendLimit() uint32 { return maxMessageBytes }

func (s *imapSession) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{Personal: []imap.NamespaceDescriptor{{Delim: imapDelim}}}, nil
}

// --- dizin yukleme ---------------------------------------------------------

// loadEntries, klasorun dizinini yukler; ham mesaji olmayan satirlari yeniden
// olusturup saklar (RFC822.SIZE ile govde uzunlugunun tutarli olmasi icin).
func (s *imapSession) loadEntries(ctx context.Context, folder string) ([]*imapMsg, error) {
	entries, err := s.srv.Store.ListMailIndex(ctx, s.acct.TenantID, s.acct.Address, folder)
	if err != nil {
		return nil, err
	}
	out := make([]*imapMsg, 0, len(entries))
	for _, e := range entries {
		if e.Size == 0 {
			raw, merr := s.materialize(ctx, e.ID)
			if merr == nil {
				e.Size = int64(len(raw))
			}
		}
		out = append(out, &imapMsg{e})
	}
	return out, nil
}

// materialize, ham mesaji bos olan kaydi yeniden olusturur ve saklar.
func (s *imapSession) materialize(ctx context.Context, id string) (string, error) {
	m, err := s.srv.Store.GetMailMessageFull(ctx, s.acct.TenantID, id)
	if err != nil {
		return "", err
	}
	if rb := m.RawData(); len(rb) > 0 {
		return string(rb), nil
	}
	metas, _ := s.srv.Store.ListMailAttachments(ctx, s.acct.TenantID, id)
	var atts []Attachment
	for _, a := range metas {
		full, gerr := s.srv.Store.GetMailAttachment(ctx, s.acct.TenantID, a.ID)
		if gerr != nil {
			continue
		}
		atts = append(atts, Attachment{Filename: full.Filename, ContentType: full.ContentType, Content: full.Content})
	}
	raw := ReconstructRaw(m, atts)
	if err := s.srv.Store.SetMailRaw(ctx, s.acct.TenantID, id, raw); err != nil {
		return "", err
	}
	return raw, nil
}

// rawFor, mesajin ham icerigini (kucuk bir onbellekle) doner.
func (s *imapSession) rawFor(ctx context.Context, m *imapMsg) ([]byte, error) {
	if b, ok := s.cache[m.ID]; ok {
		return b, nil
	}
	full, err := s.srv.Store.GetMailMessageFull(ctx, s.acct.TenantID, m.ID)
	if err != nil {
		return nil, err
	}
	b := full.RawData()
	if len(b) == 0 {
		raw, merr := s.materialize(ctx, m.ID)
		if merr != nil {
			return nil, merr
		}
		b = []byte(raw)
	}
	if int64(len(b)) != m.Size {
		m.Size = int64(len(b))
	}
	if s.cache == nil {
		s.cache = make(map[string][]byte)
	}
	s.cache[m.ID] = b
	s.cacheOrder = append(s.cacheOrder, m.ID)
	s.cacheSize += len(b)
	for (len(s.cacheOrder) > rawCacheEntries || s.cacheSize > rawCacheBytes) && len(s.cacheOrder) > 1 {
		old := s.cacheOrder[0]
		s.cacheOrder = s.cacheOrder[1:]
		s.cacheSize -= len(s.cache[old])
		delete(s.cache, old)
	}
	return b, nil
}

func (s *imapSession) dropRaw(id string) {
	if b, ok := s.cache[id]; ok {
		s.cacheSize -= len(b)
		delete(s.cache, id)
		for i, v := range s.cacheOrder {
			if v == id {
				s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
				break
			}
		}
	}
}

func (s *imapSession) notifyBox(folder string) { s.srv.Hub.Notify(s.acct.TenantID, s.acct.Address) }

// --- secim -----------------------------------------------------------------

var allFlags = []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft}

func (s *imapSession) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	f, ok, rerr := s.resolve(ctx, name)
	if rerr != nil {
		return nil, imapNo("", "Internal error")
	}
	if !ok {
		return nil, imapNo(imap.ResponseCodeNonExistent, "No such mailbox")
	}
	s.closeSelected()

	notify, unsub := s.srv.Hub.Subscribe(s.acct.TenantID, s.acct.Address)
	st, err := s.srv.Store.MailFolderState(ctx, s.acct.TenantID, s.acct.Address, f.Folder)
	if err != nil {
		unsub()
		return nil, imapNo("", "Internal error")
	}
	msgs, err := s.loadEntries(ctx, f.Folder)
	if err != nil {
		unsub()
		return nil, imapNo("", "Internal error")
	}
	s.sel = &selectedBox{f: f, readOnly: options != nil && options.ReadOnly, msgs: msgs,
		loaded: time.Now(), notify: notify, cancel: unsub}

	var first uint32
	for i, m := range msgs {
		if !m.Seen {
			first = uint32(i + 1)
			break
		}
	}
	perm := append([]imap.Flag(nil), allFlags[:4]...)
	if s.sel.readOnly {
		perm = nil
	}
	return &imap.SelectData{
		Flags:             allFlags,
		PermanentFlags:    perm,
		NumMessages:       uint32(len(msgs)),
		FirstUnseenSeqNum: first,
		UIDNext:           imap.UID(st.UIDNext),
		UIDValidity:       st.UIDValidity,
	}, nil
}

func (s *imapSession) Unselect() error {
	s.closeSelected()
	return nil
}

// --- kutu yonetimi ---------------------------------------------------------

func isSystemFolderExact(name string) bool {
	_, ok := lookupFolder(name)
	return ok
}

func mapFolderErr(err error) error {
	switch {
	case errors.Is(err, store.ErrMailFolderExists):
		return imapNo(imap.ResponseCodeAlreadyExists, "Mailbox already exists")
	case errors.Is(err, store.ErrMailFolderLimit):
		return imapNo(imap.ResponseCodeLimit, "Too many mailboxes")
	case errors.Is(err, store.ErrMailFolderInvalid):
		return imapNo(imap.ResponseCodeCannot, "Invalid mailbox name")
	case errors.Is(err, store.ErrMailFolderHasChildren):
		return imapNo(imap.ResponseCodeHasChildren, "Mailbox has child mailboxes")
	case errors.Is(err, store.ErrNotFound):
		return imapNo(imap.ResponseCodeNonExistent, "No such mailbox")
	}
	return imapNo("", "Internal error")
}

// selectedUnder, secili klasor name veya altindaysa secimi kapatir.
func (s *imapSession) dropSelectionUnder(name string) {
	if s.sel != nil && s.sel.f.User &&
		(s.sel.f.Name == name || strings.HasPrefix(s.sel.f.Name, name+"/")) {
		s.closeSelected()
	}
}

func (s *imapSession) Create(name string, options *imap.CreateOptions) error {
	// Sonda ayirac "yalniz hiyerarsi" bildirimidir; ad olarak ele alinir.
	name = strings.TrimRight(name, string(imapDelim))
	if name == "" {
		return imapNo(imap.ResponseCodeCannot, "Invalid mailbox name")
	}
	if isSystemFolderExact(name) {
		return imapNo(imap.ResponseCodeAlreadyExists, "Mailbox already exists")
	}
	if store.IsMailSystemFolderName(name) {
		return imapNo(imap.ResponseCodeCannot, "Cannot create mailboxes under a system mailbox")
	}
	use := ""
	if options != nil && len(options.SpecialUse) > 0 {
		if len(options.SpecialUse) > 1 {
			return imapNo(imap.ResponseCode("USEATTR"), "Only one special-use attribute is supported")
		}
		attr, ok := userFolderAttrs[strings.ToLower(string(options.SpecialUse[0]))]
		if !ok {
			return imapNo(imap.ResponseCode("USEATTR"), "Special-use attribute not supported")
		}
		use = string(attr)
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.srv.Store.CreateMailFolder(ctx, s.acct.TenantID, s.acct.Address, name, use); err != nil {
		return mapFolderErr(err)
	}
	return nil
}

func (s *imapSession) Delete(name string) error {
	if isSystemFolderExact(name) {
		return imapNo(imap.ResponseCodeCannot, "Cannot delete a system mailbox")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.srv.Store.DeleteMailFolder(ctx, s.acct.TenantID, s.acct.Address, name); err != nil {
		return mapFolderErr(err)
	}
	s.dropSelectionUnder(name)
	return nil
}

func (s *imapSession) Rename(oldName, newName string, _ *imap.RenameOptions) error {
	newName = strings.TrimRight(newName, string(imapDelim))
	if isSystemFolderExact(oldName) {
		return imapNo(imap.ResponseCodeCannot, "Cannot rename a system mailbox")
	}
	if isSystemFolderExact(newName) {
		return imapNo(imap.ResponseCodeAlreadyExists, "Mailbox already exists")
	}
	if store.IsMailSystemFolderName(newName) {
		return imapNo(imap.ResponseCodeCannot, "Cannot rename into a system mailbox")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.srv.Store.RenameMailFolder(ctx, s.acct.TenantID, s.acct.Address, oldName, newName); err != nil {
		return mapFolderErr(err)
	}
	s.dropSelectionUnder(oldName)
	return nil
}

func (s *imapSession) Subscribe(name string) error {
	return s.setSubscribed(name, true)
}

func (s *imapSession) Unsubscribe(name string) error {
	return s.setSubscribed(name, false)
}

func (s *imapSession) setSubscribed(name string, v bool) error {
	if isSystemFolderExact(name) {
		return nil // sistem klasorleri her zaman abonedir
	}
	ctx, cancel := s.ctx()
	defer cancel()
	err := s.srv.Store.SetMailFolderSubscribed(ctx, s.acct.TenantID, s.acct.Address, name, v)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) && !v {
			return nil // var olmayan klasorun aboneligini kaldirmak zararsiz
		}
		return mapFolderErr(err)
	}
	return nil
}

func (s *imapSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: imapDelim})
	}
	ctx, cancel := s.ctx()
	defer cancel()
	users, err := s.srv.Store.ListMailFolders(ctx, s.acct.TenantID, s.acct.Address)
	if err != nil {
		return imapNo("", "Internal error")
	}
	type entry struct {
		f          imapFolder
		subscribed bool
		children   bool
	}
	all := make([]entry, 0, len(imapFolders)+len(users))
	for _, f := range imapFolders {
		all = append(all, entry{f: f, subscribed: true})
	}
	for _, u := range users {
		e := entry{f: userImapFolder(u), subscribed: u.Subscribed}
		for _, o := range users {
			if strings.HasPrefix(o.Name, u.Name+"/") {
				e.children = true
				break
			}
		}
		all = append(all, e)
	}
	for _, e := range all {
		f := e.f
		match := false
		for _, p := range patterns {
			if imapserver.MatchList(f.Name, imapDelim, ref, p) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if options.SelectSpecialUse && f.Attr == "" {
			continue
		}
		if options.SelectSubscribed && !e.subscribed {
			continue
		}
		data := imap.ListData{Mailbox: f.Name, Delim: imapDelim}
		if e.subscribed && (options.SelectSubscribed || options.ReturnSubscribed) {
			data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
		}
		if e.children {
			data.Attrs = append(data.Attrs, imap.MailboxAttrHasChildren)
		} else {
			data.Attrs = append(data.Attrs, imap.MailboxAttrHasNoChildren)
		}
		// RFC 6154: ozel kullanim nitelikleri duz LIST yanitinda da donebilir;
		// IMAP4rev1 istemcileri (Gmail/Android) Sent/Trash'i ancak boyle bulur.
		if f.Attr != "" {
			data.Attrs = append(data.Attrs, f.Attr)
		}
		if options.ReturnStatus != nil {
			sd, err := s.statusFor(ctx, f, options.ReturnStatus)
			if err == nil {
				data.Status = sd
			}
		}
		if err := w.WriteList(&data); err != nil {
			return err
		}
	}
	return nil
}

func (s *imapSession) statusFor(ctx context.Context, f imapFolder, o *imap.StatusOptions) (*imap.StatusData, error) {
	st, err := s.srv.Store.MailFolderState(ctx, s.acct.TenantID, s.acct.Address, f.Folder)
	if err != nil {
		return nil, err
	}
	data := &imap.StatusData{Mailbox: f.Name}
	if o.UIDNext {
		data.UIDNext = imap.UID(st.UIDNext)
	}
	if o.UIDValidity {
		data.UIDValidity = st.UIDValidity
	}
	if o.NumMessages || o.NumUnseen || o.NumDeleted || o.Size {
		entries, err := s.srv.Store.ListMailIndex(ctx, s.acct.TenantID, s.acct.Address, f.Folder)
		if err != nil {
			return nil, err
		}
		var unseen, deleted uint32
		var size int64
		for _, e := range entries {
			if !e.Seen {
				unseen++
			}
			if e.Deleted {
				deleted++
			}
			size += e.Size
		}
		n := uint32(len(entries))
		if o.NumMessages {
			data.NumMessages = &n
		}
		if o.NumUnseen {
			data.NumUnseen = &unseen
		}
		if o.NumDeleted {
			data.NumDeleted = &deleted
		}
		if o.Size {
			data.Size = &size
		}
	}
	if o.NumRecent {
		z := uint32(0)
		data.NumRecent = &z
	}
	return data, nil
}

func (s *imapSession) Status(name string, options *imap.StatusOptions) (*imap.StatusData, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	f, ok, rerr := s.resolve(ctx, name)
	if rerr != nil {
		return nil, imapNo("", "Internal error")
	}
	if !ok {
		return nil, imapNo(imap.ResponseCodeNonExistent, "No such mailbox")
	}
	data, err := s.statusFor(ctx, f, options)
	if err != nil {
		return nil, imapNo("", "Internal error")
	}
	return data, nil
}

// --- yoklama / IDLE --------------------------------------------------------

func (s *imapSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return s.sync(w, allowExpunge, false)
}

func (s *imapSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.sel == nil {
		<-stop
		return nil
	}
	t := time.NewTicker(imapIdleInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-s.sel.notify:
			// Hub bildirimi: hemen yeniden yukle.
			if err := s.syncReload(w, true); err != nil {
				return err
			}
		case <-t.C:
			if err := s.syncReload(w, true); err != nil {
				return err
			}
		}
	}
}

func (s *imapSession) syncReload(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return s.sync(w, allowExpunge, true)
}

func (s *imapSession) sync(w *imapserver.UpdateWriter, allowExpunge, force bool) error {
	sel := s.sel
	if sel == nil {
		return nil
	}
	dirty := false
	select {
	case <-sel.notify:
		dirty = true
	default:
	}
	if !force && !dirty && time.Since(sel.loaded) < imapPollInterval {
		return nil
	}
	ctx, cancel := s.ctx()
	defer cancel()
	entries, err := s.loadEntries(ctx, sel.f.Folder)
	if err != nil {
		return nil // gecici hata: sonraki yoklamada tekrar dene
	}
	sel.loaded = time.Now()
	cur := make(map[uint32]*imapMsg, len(entries))
	for _, e := range entries {
		cur[e.UID] = e
	}
	if allowExpunge {
		for i := len(sel.msgs) - 1; i >= 0; i-- {
			if _, ok := cur[sel.msgs[i].UID]; !ok {
				if err := w.WriteExpunge(uint32(i + 1)); err != nil {
					return err
				}
				s.dropRaw(sel.msgs[i].ID)
				sel.msgs = append(sel.msgs[:i], sel.msgs[i+1:]...)
			}
		}
	}
	for i, m := range sel.msgs {
		e, ok := cur[m.UID]
		if !ok {
			continue
		}
		if e.Seen != m.Seen || e.Flagged != m.Flagged || e.Answered != m.Answered || e.Deleted != m.Deleted {
			m.Seen, m.Flagged, m.Answered, m.Deleted = e.Seen, e.Flagged, e.Answered, e.Deleted
			if err := w.WriteMessageFlags(uint32(i+1), imap.UID(m.UID), m.flags(sel.f.Folder)); err != nil {
				return err
			}
		}
	}
	var maxUID uint32
	if n := len(sel.msgs); n > 0 {
		maxUID = sel.msgs[n-1].UID
	}
	added := false
	for _, e := range entries {
		if e.UID > maxUID {
			sel.msgs = append(sel.msgs, e)
			added = true
		}
	}
	if added {
		return w.WriteNumMessages(uint32(len(sel.msgs)))
	}
	return nil
}

// --- numara kumeleri -------------------------------------------------------

func rangeHas(start, stop, max, v uint32) bool {
	if start == 0 {
		start = max
	}
	if stop == 0 {
		stop = max
	}
	if start > stop {
		start, stop = stop, start
	}
	return v >= start && v <= stop
}

// matching, numara kumesine uyan mesajlarin dizin konumlarini doner.
func (b *selectedBox) matching(numSet imap.NumSet) []int {
	var maxUID uint32
	if n := len(b.msgs); n > 0 {
		maxUID = b.msgs[n-1].UID
	}
	maxSeq := uint32(len(b.msgs))
	var out []int
	for i, m := range b.msgs {
		seq := uint32(i + 1)
		hit := false
		switch ns := numSet.(type) {
		case imap.SeqSet:
			for _, r := range ns {
				if rangeHas(r.Start, r.Stop, maxSeq, seq) {
					hit = true
					break
				}
			}
		case imap.UIDSet:
			for _, r := range ns {
				if rangeHas(uint32(r.Start), uint32(r.Stop), maxUID, m.UID) {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, i)
		}
	}
	return out
}

func (s *imapSession) requireWritable() error {
	if s.sel == nil {
		return imapNo("", "No mailbox selected")
	}
	if s.sel.readOnly {
		return imapNo(imap.ResponseCode("READ-ONLY"), "Mailbox is read-only")
	}
	return nil
}

// --- FETCH -----------------------------------------------------------------

func (s *imapSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	sel := s.sel
	if sel == nil {
		return imapNo("", "No mailbox selected")
	}
	ctx, cancel := s.ctx()
	defer cancel()

	markSeen := false
	if !sel.readOnly {
		for _, bs := range options.BodySection {
			if !bs.Peek {
				markSeen = true
			}
		}
		for _, bs := range options.BinarySection {
			if !bs.Peek {
				markSeen = true
			}
		}
	}
	needRaw := options.Envelope || options.BodyStructure != nil || len(options.BodySection) > 0 ||
		len(options.BinarySection) > 0 || len(options.BinarySectionSize) > 0

	for _, i := range sel.matching(numSet) {
		m := sel.msgs[i]
		flagsChanged := false
		if markSeen && !m.Seen {
			t := true
			if err := s.srv.Store.SetMailFlags(ctx, s.acct.TenantID, m.ID, store.MailFlagUpdate{Seen: &t}); err == nil {
				m.Seen = true
				flagsChanged = true
			}
		}
		var raw []byte
		if needRaw {
			b, err := s.rawFor(ctx, m)
			if err != nil {
				// Mesaj arada silinmis olabilir: bu mesaji atla.
				continue
			}
			raw = b
		}
		rw := w.CreateMessage(uint32(i + 1))
		rw.WriteUID(imap.UID(m.UID))
		if options.Flags || flagsChanged {
			rw.WriteFlags(m.flags(sel.f.Folder))
		}
		if options.InternalDate {
			rw.WriteInternalDate(m.ReceivedAt)
		}
		if options.RFC822Size {
			size := m.Size
			if raw != nil {
				size = int64(len(raw))
			}
			rw.WriteRFC822Size(size)
		}
		if options.Envelope {
			if h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw))); err == nil {
				rw.WriteEnvelope(imapserver.ExtractEnvelope(h))
			} else {
				rw.WriteEnvelope(&imap.Envelope{})
			}
		}
		if options.BodyStructure != nil {
			rw.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
		}
		for _, bs := range options.BodySection {
			buf := imapserver.ExtractBodySection(bytes.NewReader(raw), bs)
			wc := rw.WriteBodySection(bs, int64(len(buf)))
			_, werr := wc.Write(buf)
			cerr := wc.Close()
			if werr != nil {
				return werr
			}
			if cerr != nil {
				return cerr
			}
		}
		for _, bs := range options.BinarySection {
			buf := imapserver.ExtractBinarySection(bytes.NewReader(raw), bs)
			wc := rw.WriteBinarySection(bs, int64(len(buf)))
			_, werr := wc.Write(buf)
			cerr := wc.Close()
			if werr != nil {
				return werr
			}
			if cerr != nil {
				return cerr
			}
		}
		for _, bss := range options.BinarySectionSize {
			rw.WriteBinarySectionSize(bss, imapserver.ExtractBinarySectionSize(bytes.NewReader(raw), bss))
		}
		if err := rw.Close(); err != nil {
			return err
		}
	}
	if markSeen {
		s.notifyBox(sel.f.Folder)
	}
	return nil
}

// --- STORE -----------------------------------------------------------------

func (s *imapSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	sel := s.sel
	ctx, cancel := s.ctx()
	defer cancel()

	for _, i := range sel.matching(numSet) {
		m := sel.msgs[i]
		target := struct{ seen, flagged, answered, deleted bool }{m.Seen, m.Flagged, m.Answered, m.Deleted}
		set := func(f imap.Flag, v bool) {
			switch strings.ToLower(string(f)) {
			case strings.ToLower(string(imap.FlagSeen)):
				target.seen = v
			case strings.ToLower(string(imap.FlagFlagged)):
				target.flagged = v
			case strings.ToLower(string(imap.FlagAnswered)):
				target.answered = v
			case strings.ToLower(string(imap.FlagDeleted)):
				target.deleted = v
			}
		}
		switch flags.Op {
		case imap.StoreFlagsSet:
			target.seen, target.flagged, target.answered, target.deleted = false, false, false, false
			fallthrough
		case imap.StoreFlagsAdd:
			for _, f := range flags.Flags {
				set(f, true)
			}
		case imap.StoreFlagsDel:
			for _, f := range flags.Flags {
				set(f, false)
			}
		}
		var upd store.MailFlagUpdate
		changed := false
		if target.seen != m.Seen {
			v := target.seen
			upd.Seen, changed = &v, true
		}
		if target.flagged != m.Flagged {
			v := target.flagged
			upd.Flagged, changed = &v, true
		}
		if target.answered != m.Answered {
			v := target.answered
			upd.Answered, changed = &v, true
		}
		if target.deleted != m.Deleted {
			v := target.deleted
			upd.Deleted, changed = &v, true
		}
		if changed {
			if err := s.srv.Store.SetMailFlags(ctx, s.acct.TenantID, m.ID, upd); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return imapNo("", "Internal error")
			}
			m.Seen, m.Flagged, m.Answered, m.Deleted = target.seen, target.flagged, target.answered, target.deleted
		}
		if !flags.Silent {
			rw := w.CreateMessage(uint32(i + 1))
			rw.WriteUID(imap.UID(m.UID))
			rw.WriteFlags(m.flags(sel.f.Folder))
			if err := rw.Close(); err != nil {
				return err
			}
		}
	}
	s.notifyBox(sel.f.Folder)
	return nil
}

// --- EXPUNGE ---------------------------------------------------------------

func (s *imapSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	sel := s.sel
	ctx, cancel := s.ctx()
	defer cancel()

	var maxUID uint32
	if n := len(sel.msgs); n > 0 {
		maxUID = sel.msgs[n-1].UID
	}
	var victims []int
	var victimUIDs []uint32
	for i, m := range sel.msgs {
		if !m.Deleted {
			continue
		}
		if uids != nil {
			hit := false
			for _, r := range *uids {
				if rangeHas(uint32(r.Start), uint32(r.Stop), maxUID, m.UID) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		victims = append(victims, i)
		victimUIDs = append(victimUIDs, m.UID)
	}
	if len(victims) == 0 {
		return nil
	}
	if _, err := s.srv.Store.ExpungeMail(ctx, s.acct.TenantID, s.acct.Address, sel.f.Folder, victimUIDs); err != nil {
		return imapNo("", "Internal error")
	}
	// Yuksek sira numarasindan baslayarak bildir (numaralar kaymasin).
	for k := len(victims) - 1; k >= 0; k-- {
		i := victims[k]
		if err := w.WriteExpunge(uint32(i + 1)); err != nil {
			return err
		}
		s.dropRaw(sel.msgs[i].ID)
		sel.msgs = append(sel.msgs[:i], sel.msgs[i+1:]...)
	}
	s.notifyBox(sel.f.Folder)
	return nil
}

// --- COPY / MOVE -----------------------------------------------------------

func (s *imapSession) destFolder(ctx context.Context, name string) (imapFolder, error) {
	f, ok, rerr := s.resolve(ctx, name)
	if rerr != nil {
		return imapFolder{}, imapNo("", "Internal error")
	}
	if !ok {
		return imapFolder{}, imapNo(imap.ResponseCodeTryCreate, "No such mailbox")
	}
	if s.sel != nil && f.Folder == s.sel.f.Folder {
		return imapFolder{}, imapNo("", "Source and destination mailboxes are identical")
	}
	return f, nil
}

func (s *imapSession) Copy(numSet imap.NumSet, destName string) (*imap.CopyData, error) {
	if s.sel == nil {
		return nil, imapNo("", "No mailbox selected")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	dest, err := s.destFolder(ctx, destName)
	if err != nil {
		return nil, err
	}
	var src, dst imap.UIDSet
	for _, i := range s.sel.matching(numSet) {
		m := s.sel.msgs[i]
		full, gerr := s.srv.Store.GetMailMessageFull(ctx, s.acct.TenantID, m.ID)
		if gerr != nil {
			continue
		}
		if len(full.RawData()) == 0 {
			if raw, merr := s.materialize(ctx, m.ID); merr == nil {
				full.RawBytes = []byte(raw)
			}
		}
		clone := full
		clone.ID = ""
		clone.Folder = dest.Folder
		clone.Mailbox = s.acct.Address
		clone.Deleted = false
		saved, ierr := s.srv.Store.InsertMailMessage(ctx, clone)
		if ierr != nil {
			return nil, imapNo("", "Internal error")
		}
		s.copyAttachments(ctx, m.ID, saved.ID)
		src.AddNum(imap.UID(m.UID))
		dst.AddNum(imap.UID(saved.UID))
	}
	st, _ := s.srv.Store.MailFolderState(ctx, s.acct.TenantID, s.acct.Address, dest.Folder)
	s.notifyBox(dest.Folder)
	return &imap.CopyData{UIDValidity: st.UIDValidity, SourceUIDs: src, DestUIDs: dst}, nil
}

func (s *imapSession) copyAttachments(ctx context.Context, fromID, toID string) {
	metas, _ := s.srv.Store.ListMailAttachments(ctx, s.acct.TenantID, fromID)
	for _, a := range metas {
		full, err := s.srv.Store.GetMailAttachment(ctx, s.acct.TenantID, a.ID)
		if err != nil {
			continue
		}
		_ = s.srv.Store.InsertMailAttachment(ctx, store.MailAttachment{
			MessageID: toID, TenantID: s.acct.TenantID, Filename: full.Filename,
			ContentType: full.ContentType, SizeBytes: full.SizeBytes, Content: full.Content,
		})
	}
}

func (s *imapSession) Move(w *imapserver.MoveWriter, numSet imap.NumSet, destName string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	sel := s.sel
	ctx, cancel := s.ctx()
	defer cancel()
	dest, err := s.destFolder(ctx, destName)
	if err != nil {
		return err
	}
	var src, dst imap.UIDSet
	var moved []int
	for _, i := range sel.matching(numSet) {
		m := sel.msgs[i]
		newUID, merr := s.srv.Store.MoveMailMessage(ctx, s.acct.TenantID, m.ID, dest.Folder)
		if merr != nil {
			if errors.Is(merr, store.ErrNotFound) {
				continue
			}
			return imapNo("", "Internal error")
		}
		src.AddNum(imap.UID(m.UID))
		dst.AddNum(imap.UID(newUID))
		moved = append(moved, i)
	}
	st, _ := s.srv.Store.MailFolderState(ctx, s.acct.TenantID, s.acct.Address, dest.Folder)
	if err := w.WriteCopyData(&imap.CopyData{UIDValidity: st.UIDValidity, SourceUIDs: src, DestUIDs: dst}); err != nil {
		return err
	}
	for k := len(moved) - 1; k >= 0; k-- {
		i := moved[k]
		if err := w.WriteExpunge(uint32(i + 1)); err != nil {
			return err
		}
		s.dropRaw(sel.msgs[i].ID)
		sel.msgs = append(sel.msgs[:i], sel.msgs[i+1:]...)
	}
	s.notifyBox(dest.Folder)
	return nil
}

// --- APPEND ----------------------------------------------------------------

func (s *imapSession) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	f, ok, rerr := s.resolve(ctx, mailbox)
	if rerr != nil {
		return nil, imapNo("", "Internal error")
	}
	if !ok {
		return nil, imapNo(imap.ResponseCodeTryCreate, "No such mailbox")
	}
	if r.Size() > maxMessageBytes {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeTooBig, Text: "Message too large"}
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxMessageBytes+1))
	if err != nil {
		return nil, imapNo("", "Could not read message")
	}
	if len(raw) > maxMessageBytes {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeTooBig, Text: "Message too large"}
	}

	st, err := s.srv.Store.MailFolderState(ctx, s.acct.TenantID, s.acct.Address, f.Folder)
	if err != nil {
		return nil, imapNo("", "Internal error")
	}

	// Gonderilenler: SMTP gonderim sunucusu kopyayi zaten kaydetmis olabilir —
	// ayni Message-ID varsa tekrar ekleme.
	mid := HeaderValue(raw, "Message-Id")
	if f.Folder == store.MailFolderSent && mid != "" {
		if e, found, ferr := s.srv.Store.FindMailByMessageID(ctx, s.acct.TenantID, s.acct.Address, f.Folder, mid); ferr == nil && found {
			return &imap.AppendData{UID: imap.UID(e.UID), UIDValidity: st.UIDValidity}, nil
		}
	}

	saved, err := s.srv.insertParsed(ctx, s.acct, f.Folder, raw, appendFlags(options, f.Folder))
	if err != nil {
		return nil, imapNo("", "Internal error")
	}
	s.notifyBox(f.Folder)
	return &imap.AppendData{UID: imap.UID(saved.UID), UIDValidity: st.UIDValidity}, nil
}

type appendFlagSet struct{ seen, flagged, answered, deleted bool }

func appendFlags(o *imap.AppendOptions, folder string) appendFlagSet {
	var fs appendFlagSet
	if o != nil {
		for _, f := range o.Flags {
			switch strings.ToLower(string(f)) {
			case strings.ToLower(string(imap.FlagSeen)):
				fs.seen = true
			case strings.ToLower(string(imap.FlagFlagged)):
				fs.flagged = true
			case strings.ToLower(string(imap.FlagAnswered)):
				fs.answered = true
			case strings.ToLower(string(imap.FlagDeleted)):
				fs.deleted = true
			}
		}
	}
	if folder == store.MailFolderSent || folder == store.MailFolderDrafts {
		fs.seen = true
	}
	return fs
}

// insertParsed, ham bir mesaji ayristirip klasore (ekleriyle) kaydeder.
func (s *IMAPServer) insertParsed(ctx context.Context, acct *Account, folder string, raw []byte, fl appendFlagSet) (store.MailMessage, error) {
	return storeRawMessage(ctx, s.Store, acct, folder, raw, fl)
}

// storeRawMessage, IMAP APPEND ve SMTP gonderim kopyasi icin ortak kayit yolu.
func storeRawMessage(ctx context.Context, st store.Store, acct *Account, folder string, raw []byte, fl appendFlagSet) (store.MailMessage, error) {
	subject, fromAddr, text, html, messageID, inReplyTo, attachments := parseMessage(raw)
	if fromAddr == "" {
		fromAddr = acct.Address
	}
	to := strings.Join(HeaderAddresses(raw, "To"), ", ")
	direction := "outbound"
	if folder == store.MailFolderInbox || folder == store.MailFolderTrash {
		direction = "inbound"
	}
	if _, user := store.MailUserFolderName(folder); user && strings.EqualFold(strings.TrimSpace(fromAddr), acct.Address) {
		direction = "outbound"
	} else if user {
		direction = "inbound"
	}
	if direction == "inbound" && to == "" {
		to = acct.Address
	}
	saved, err := st.InsertMailMessage(ctx, store.MailMessage{
		TenantID:  acct.TenantID,
		Direction: direction,
		From:      fromAddr,
		To:        to,
		Subject:   subject,
		TextBody:  text,
		HTMLBody:  html,
		MessageID: messageID,
		InReplyTo: inReplyTo,
		Seen:      fl.seen,
		Flagged:   fl.flagged,
		Answered:  fl.answered,
		Deleted:   fl.deleted,
		RawBytes:  raw,
		Folder:    folder,
		Mailbox:   acct.Address,
	})
	if err != nil {
		return store.MailMessage{}, err
	}
	for _, att := range attachments {
		att.MessageID = saved.ID
		att.TenantID = acct.TenantID
		_ = st.InsertMailAttachment(ctx, att)
	}
	return saved, nil
}

// imapDebugWriter, ZORVEN_IMAP_DEBUG=1 iken IMAP protokol trafigini (sorun
// giderme icin) slog'a satir satir yazar. LOGIN/AUTHENTICATE satirlarindaki
// parolalar gizlenir. Varsayilan kapali: ileti govdeleri de loga duser.
func imapDebugWriter(l *slog.Logger) io.Writer {
	if os.Getenv("ZORVEN_IMAP_DEBUG") != "1" || l == nil {
		return nil
	}
	return &imapTrace{l: l}
}

type imapTrace struct {
	l   *slog.Logger
	mu  sync.Mutex
	buf []byte
}

func (t *imapTrace) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, 10)
		if i < 0 {
			if len(t.buf) > 4096 {
				t.emit(t.buf)
				t.buf = t.buf[:0]
			}
			return len(p), nil
		}
		t.emit(t.buf[:i])
		t.buf = t.buf[i+1:]
	}
}

func (t *imapTrace) emit(line []byte) {
	s := strings.TrimRight(string(line), "\r")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	up := strings.ToUpper(s)
	if i := strings.Index(up, " LOGIN "); i >= 0 {
		rest := strings.Fields(s[i+7:])
		user := ""
		if len(rest) > 0 {
			user = rest[0]
		}
		s = s[:i] + " LOGIN " + user + " ***"
	} else if strings.Contains(up, "AUTHENTICATE") || (len(s) > 20 && !strings.ContainsAny(s, " ()")) {
		s = "[gizlendi]"
	}
	t.l.Info("imap-trace", "satir", s)
}
