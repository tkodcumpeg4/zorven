package mail

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// memMailStore, IMAP/SMTP testleri icin Postgres'siz, bellekte calisan store.
// Yalnizca ilgili yontemler uygulanir; digerleri (gomulu nil arayuz) cagrilirsa panic eder.
type memMailStore struct {
	store.Store

	mu       sync.Mutex
	seq      int
	msgs     map[string]*store.MailMessage
	lastUID  map[string]uint32 // tenant|mailbox|folder
	validity map[string]uint32
	atts     map[string][]store.MailAttachment // message id
	tenants  map[string]store.Tenant           // slug -> tenant
	plans    map[string]string                 // tenant -> plan
	pws      []store.MailAppPassword
	folders  map[string][]store.MailFolder
}

func newMemMailStore() *memMailStore {
	return &memMailStore{
		msgs:     map[string]*store.MailMessage{},
		lastUID:  map[string]uint32{},
		validity: map[string]uint32{},
		atts:     map[string][]store.MailAttachment{},
		tenants:  map[string]store.Tenant{},
		plans:    map[string]string{},
	}
}

func (m *memMailStore) addTenant(id, slug, plan string) {
	m.tenants[slug] = store.Tenant{ID: id, Slug: slug}
	m.plans[id] = plan
}

func (m *memMailStore) addAppPassword(tenant, mailbox, password string) store.MailAppPassword {
	h, err := HashAppPassword(password)
	if err != nil {
		panic(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	p := store.MailAppPassword{ID: fmt.Sprintf("pw%d", m.seq), TenantID: tenant, Mailbox: mailbox, Label: "test", PasswordHash: h, CreatedAt: time.Now()}
	m.pws = append(m.pws, p)
	return p
}

func (m *memMailStore) revokePassword(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.pws {
		if m.pws[i].ID == id {
			now := time.Now()
			m.pws[i].RevokedAt = &now
		}
	}
}

func (m *memMailStore) passwordByID(id string) store.MailAppPassword {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pws {
		if p.ID == id {
			return p
		}
	}
	return store.MailAppPassword{}
}

func key(t, mb, f string) string { return t + "|" + strings.ToLower(mb) + "|" + f }

func (m *memMailStore) GetTenantBySlug(_ context.Context, slug string) (store.Tenant, error) {
	t, ok := m.tenants[slug]
	if !ok {
		return store.Tenant{}, store.ErrNotFound
	}
	return t, nil
}

func (m *memMailStore) GetSubscription(_ context.Context, tenantID string) (store.Subscription, error) {
	return store.Subscription{TenantID: tenantID, Plan: m.plans[tenantID], Status: "active"}, nil
}

func (m *memMailStore) nextUIDLocked(t, mb, f string) uint32 {
	k := key(t, mb, f)
	m.lastUID[k]++
	if m.validity[k] == 0 {
		m.validity[k] = 1000 + uint32(len(m.validity))
	}
	return m.lastUID[k]
}

func (m *memMailStore) InsertMailMessage(_ context.Context, in store.MailMessage) (store.MailMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	in.ID = fmt.Sprintf("mm%d", m.seq)
	if in.Folder == "" {
		in.Folder = store.MailFolderInbox
		if in.Direction == "outbound" {
			in.Folder = store.MailFolderSent
		}
	}
	if in.Mailbox == "" {
		in.Mailbox = in.To
		if in.Direction == "outbound" {
			in.Mailbox = in.From
		}
	}
	in.Mailbox = strings.ToLower(in.Mailbox)
	in.UID = m.nextUIDLocked(in.TenantID, in.Mailbox, in.Folder)
	in.ReceivedAt = time.Now().Add(time.Duration(m.seq) * time.Second)
	cp := in
	m.msgs[in.ID] = &cp
	return in, nil
}

func (m *memMailStore) MailFolderState(_ context.Context, t, mb, f string) (store.MailFolderState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(t, mb, f)
	if m.validity[k] == 0 {
		m.validity[k] = 1000 + uint32(len(m.validity))
	}
	return store.MailFolderState{UIDValidity: m.validity[k], UIDNext: m.lastUID[k] + 1}, nil
}

func (m *memMailStore) ListMailIndex(_ context.Context, t, mb, f string) ([]store.MailIndexEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.MailIndexEntry
	for _, x := range m.msgs {
		if x.TenantID == t && x.Mailbox == strings.ToLower(mb) && x.Folder == f {
			out = append(out, store.MailIndexEntry{ID: x.ID, UID: x.UID, Seen: x.Seen, Flagged: x.Flagged,
				Answered: x.Answered, Deleted: x.Deleted, Size: int64(len(x.RawData())), ReceivedAt: x.ReceivedAt, MessageID: x.MessageID})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (m *memMailStore) GetMailMessageFull(_ context.Context, t, id string) (store.MailMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.msgs[id]
	if !ok || x.TenantID != t {
		return store.MailMessage{}, store.ErrNotFound
	}
	return *x, nil
}

func (m *memMailStore) get(id string) store.MailMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x, ok := m.msgs[id]; ok {
		return *x
	}
	return store.MailMessage{}
}

func (m *memMailStore) all(t, mb, f string) []store.MailMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.MailMessage
	for _, x := range m.msgs {
		if x.TenantID == t && x.Mailbox == strings.ToLower(mb) && x.Folder == f {
			out = append(out, *x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out
}

func (m *memMailStore) SetMailRaw(_ context.Context, t, id, raw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.msgs[id]
	if !ok || x.TenantID != t {
		return store.ErrNotFound
	}
	x.Raw = ""
	x.RawBytes = []byte(raw)
	return nil
}

func (m *memMailStore) SetMailFlags(_ context.Context, t, id string, u store.MailFlagUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.msgs[id]
	if !ok || x.TenantID != t {
		return store.ErrNotFound
	}
	if u.Seen != nil {
		x.Seen = *u.Seen
	}
	if u.Flagged != nil {
		x.Flagged = *u.Flagged
	}
	if u.Answered != nil {
		x.Answered = *u.Answered
	}
	if u.Deleted != nil {
		x.Deleted = *u.Deleted
	}
	return nil
}

func (m *memMailStore) MoveMailMessage(_ context.Context, t, id, folder string) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.msgs[id]
	if !ok || x.TenantID != t {
		return 0, store.ErrNotFound
	}
	if x.Folder == folder {
		return x.UID, nil
	}
	x.UID = m.nextUIDLocked(t, x.Mailbox, folder)
	x.Folder = folder
	x.Deleted = false
	return x.UID, nil
}

func (m *memMailStore) ExpungeMail(_ context.Context, t, mb, f string, uids []uint32) ([]uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uint32
	for id, x := range m.msgs {
		if x.TenantID != t || x.Mailbox != strings.ToLower(mb) || x.Folder != f || !x.Deleted {
			continue
		}
		if len(uids) > 0 {
			hit := false
			for _, u := range uids {
				hit = hit || u == x.UID
			}
			if !hit {
				continue
			}
		}
		out = append(out, x.UID)
		delete(m.msgs, id)
	}
	return out, nil
}

func (m *memMailStore) FindMailByMessageID(_ context.Context, t, mb, f, mid string) (store.MailIndexEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.msgs {
		if mid != "" && x.TenantID == t && x.Mailbox == strings.ToLower(mb) && x.Folder == f && x.MessageID == mid {
			return store.MailIndexEntry{ID: x.ID, UID: x.UID, MessageID: x.MessageID}, true, nil
		}
	}
	return store.MailIndexEntry{}, false, nil
}

func (m *memMailStore) InsertMailAttachment(_ context.Context, a store.MailAttachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	a.ID = fmt.Sprintf("att%d", m.seq)
	m.atts[a.MessageID] = append(m.atts[a.MessageID], a)
	return nil
}

func (m *memMailStore) ListMailAttachments(_ context.Context, t, msgID string) ([]store.MailAttachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.MailAttachment
	for _, a := range m.atts[msgID] {
		c := a
		c.Content = nil
		out = append(out, c)
	}
	return out, nil
}

func (m *memMailStore) GetMailAttachment(_ context.Context, t, id string) (store.MailAttachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, list := range m.atts {
		for _, a := range list {
			if a.ID == id {
				return a, nil
			}
		}
	}
	return store.MailAttachment{}, store.ErrNotFound
}

func (m *memMailStore) ListActiveMailAppPasswords(_ context.Context, mailbox string) ([]store.MailAppPassword, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.MailAppPassword
	for _, p := range m.pws {
		if p.Mailbox == strings.ToLower(mailbox) && p.RevokedAt == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memMailStore) TouchMailAppPassword(_ context.Context, id, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.pws {
		if m.pws[i].ID == id {
			now := time.Now()
			m.pws[i].LastUsedAt = &now
			m.pws[i].LastUsedIP = ip
		}
	}
	return nil
}
