package mail

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Kullanici klasorleri icin bellek ici uygulama (pgstore/mail_folders.go ile ayni anlam).

func (m *memMailStore) folderList(t, mb string) []store.MailFolder {
	if m.folders == nil {
		m.folders = map[string][]store.MailFolder{}
	}
	return m.folders[t+"|"+strings.ToLower(mb)]
}

func (m *memMailStore) setFolderList(t, mb string, l []store.MailFolder) {
	m.folders[t+"|"+strings.ToLower(mb)] = l
}

func (m *memMailStore) findFolderCI(l []store.MailFolder, name string) int {
	for i, f := range l {
		if strings.EqualFold(f.Name, name) {
			return i
		}
	}
	return -1
}

func (m *memMailStore) CreateMailFolder(_ context.Context, t, mb, name, use string) error {
	if err := store.ValidateMailFolderName(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.folderList(t, mb)
	if m.findFolderCI(l, name) >= 0 {
		return store.ErrMailFolderExists
	}
	var add []store.MailFolder
	for _, p := range store.MailFolderParents(name) {
		if m.findFolderCI(l, p) < 0 {
			add = append(add, store.MailFolder{Name: p, Subscribed: true, CreatedAt: time.Now()})
		}
	}
	if len(l)+len(add)+1 > store.MaxMailFoldersPerMailbox {
		return store.ErrMailFolderLimit
	}
	add = append(add, store.MailFolder{Name: name, SpecialUse: use, Subscribed: true, CreatedAt: time.Now()})
	m.setFolderList(t, mb, append(l, add...))
	return nil
}

func (m *memMailStore) ListMailFolders(_ context.Context, t, mb string) ([]store.MailFolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]store.MailFolder(nil), m.folderList(t, mb)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memMailStore) ListTenantMailFolderNames(_ context.Context, t string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for k, l := range m.folders {
		if strings.HasPrefix(k, t+"|") {
			for _, f := range l {
				if !seen[f.Name] {
					seen[f.Name] = true
					out = append(out, f.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *memMailStore) bumpValidityLocked(t, mb, folder string) {
	k := key(t, mb, folder)
	m.lastUID[k] = 0
	m.validity[k] = m.validity[k] + 1000
}

func (m *memMailStore) DeleteMailFolder(_ context.Context, t, mb, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.folderList(t, mb)
	idx := -1
	for i, f := range l {
		if f.Name == name {
			idx = i
		}
		if strings.HasPrefix(f.Name, name+"/") {
			return store.ErrMailFolderHasChildren
		}
	}
	if idx < 0 {
		return store.ErrNotFound
	}
	fk := store.MailUserFolderKey(name)
	for id, x := range m.msgs {
		if x.TenantID == t && x.Mailbox == strings.ToLower(mb) && x.Folder == fk {
			delete(m.msgs, id)
		}
	}
	m.bumpValidityLocked(t, mb, fk)
	m.setFolderList(t, mb, append(l[:idx:idx], l[idx+1:]...))
	return nil
}

func (m *memMailStore) RenameMailFolder(_ context.Context, t, mb, oldName, newName string) error {
	if err := store.ValidateMailFolderName(newName); err != nil {
		return err
	}
	if newName == oldName || strings.HasPrefix(newName, oldName+"/") {
		return store.ErrMailFolderInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.folderList(t, mb)
	found := false
	for _, f := range l {
		if f.Name == oldName {
			found = true
		}
	}
	if !found {
		return store.ErrNotFound
	}
	if i := m.findFolderCI(l, newName); i >= 0 && !strings.EqualFold(l[i].Name, oldName) {
		return store.ErrMailFolderExists
	}
	for _, p := range store.MailFolderParents(newName) {
		if p == oldName || strings.HasPrefix(p, oldName+"/") {
			return store.ErrMailFolderInvalid
		}
		if m.findFolderCI(l, p) < 0 {
			l = append(l, store.MailFolder{Name: p, Subscribed: true, CreatedAt: time.Now()})
		}
	}
	for i, f := range l {
		if f.Name != oldName && !strings.HasPrefix(f.Name, oldName+"/") {
			continue
		}
		dst := newName + f.Name[len(oldName):]
		oldKey, newKey := store.MailUserFolderKey(f.Name), store.MailUserFolderKey(dst)
		for _, x := range m.msgs {
			if x.TenantID == t && x.Mailbox == strings.ToLower(mb) && x.Folder == oldKey {
				x.Folder = newKey
			}
		}
		ok, nk := key(t, mb, oldKey), key(t, mb, newKey)
		m.lastUID[nk] = m.lastUID[ok]
		if m.validity[ok] != 0 {
			m.validity[nk] = m.validity[ok]
		}
		m.bumpValidityLocked(t, mb, oldKey)
		l[i].Name = dst
	}
	m.setFolderList(t, mb, l)
	return nil
}

func (m *memMailStore) SetMailFolderSubscribed(_ context.Context, t, mb, name string, v bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.folderList(t, mb)
	for i := range l {
		if l[i].Name == name {
			l[i].Subscribed = v
			return nil
		}
	}
	return store.ErrNotFound
}
