package oauthas

import (
	"context"
	"sync"
	"time"
)

// MemStore, testler ve gelistirme icin bellek ici Store.
type MemStore struct {
	mu      sync.Mutex
	clients map[string]Client
	codes   map[string]*memCode
	grants  map[string]Grant
	refresh map[string]*memRefresh
}

type memCode struct {
	Code
	used bool
}

type memRefresh struct {
	grantID string
	expires time.Time
	used    bool
}

// NewMemStore, bos bir bellek deposu doner.
func NewMemStore() *MemStore {
	return &MemStore{clients: map[string]Client{}, codes: map[string]*memCode{},
		grants: map[string]Grant{}, refresh: map[string]*memRefresh{}}
}

func (m *MemStore) CreateClient(_ context.Context, c Client) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c.ID] = c
	return nil
}

func (m *MemStore) GetClient(_ context.Context, id string) (Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[id]
	if !ok {
		return Client{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) CountClients(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients), nil
}

func (m *MemStore) CreateCode(_ context.Context, hash string, c Code) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[hash] = &memCode{Code: c}
	return nil
}

func (m *MemStore) ConsumeCode(_ context.Context, hash string, now time.Time) (Code, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[hash]
	if !ok || now.After(c.ExpiresAt) {
		return Code{}, ErrNotFound
	}
	if c.used {
		return Code{}, ErrUsed
	}
	c.used = true
	return c.Code, nil
}

func (m *MemStore) CreateGrant(_ context.Context, g Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grants[g.ID] = g
	return nil
}

func (m *MemStore) withName(g Grant) Grant {
	g.ClientName = m.clients[g.ClientID].Name
	return g
}

func (m *MemStore) GetGrant(_ context.Context, id string) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grants[id]
	if !ok {
		return Grant{}, ErrNotFound
	}
	return m.withName(g), nil
}

func (m *MemStore) TouchGrant(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.grants[id]; ok {
		g.LastUsedAt = &now
		m.grants[id] = g
	}
	return nil
}

func (m *MemStore) RevokeGrant(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.grants[id]; ok && g.RevokedAt == nil {
		g.RevokedAt = &now
		m.grants[id] = g
	}
	for h, r := range m.refresh {
		if r.grantID == id {
			delete(m.refresh, h)
		}
	}
	return nil
}

func (m *MemStore) ListGrants(_ context.Context, userID, tenantID string) ([]Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Grant
	for _, g := range m.grants {
		if g.RevokedAt != nil || (userID != "" && g.UserID != userID) || (tenantID != "" && g.TenantID != tenantID) {
			continue
		}
		out = append(out, m.withName(g))
	}
	return out, nil
}

func (m *MemStore) SaveRefresh(_ context.Context, hash, grantID string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[hash] = &memRefresh{grantID: grantID, expires: expires}
	return nil
}

func (m *MemStore) RotateRefresh(_ context.Context, hash string, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refresh[hash]
	if !ok || now.After(r.expires) {
		return "", ErrNotFound
	}
	if r.used {
		return r.grantID, ErrUsed
	}
	r.used = true
	return r.grantID, nil
}

func (m *MemStore) Prune(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, c := range m.codes {
		if now.After(c.ExpiresAt.Add(10 * time.Minute)) {
			delete(m.codes, h)
		}
	}
	for h, r := range m.refresh {
		if now.After(r.expires) {
			delete(m.refresh, h)
		}
	}
	return nil
}
