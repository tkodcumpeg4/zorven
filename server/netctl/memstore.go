package netctl

import (
	"context"
	"encoding/json"
	"sync"
)

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// MemStore, bellek ici Store (testler ve kalicilik gerektirmeyen kurulumlar).
type MemStore struct {
	mu     sync.Mutex
	nodes  map[string]NodeRec // tenant|id
	shares map[string]Share
}

func NewMemStore() *MemStore {
	return &MemStore{nodes: map[string]NodeRec{}, shares: map[string]Share{}}
}

func (s *MemStore) ListNodes(_ context.Context, tenantID string) ([]NodeRec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []NodeRec
	for _, n := range s.nodes {
		if n.TenantID == tenantID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *MemStore) PutNode(_ context.Context, n NodeRec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[n.TenantID+"|"+n.ID] = n
	return nil
}

func (s *MemStore) DeleteNode(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, tenantID+"|"+id)
	return nil
}

func (s *MemStore) ListShares(_ context.Context, tenantID string) ([]Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Share
	for _, x := range s.shares {
		if x.TenantID == tenantID {
			out = append(out, x)
		}
	}
	return out, nil
}

func (s *MemStore) PutShare(_ context.Context, x Share) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shares[x.TenantID+"|"+x.ID] = x
	return nil
}

func (s *MemStore) DeleteShare(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.shares, tenantID+"|"+id)
	return nil
}

// MapDirectory, testler icin basit dizin.
type MapDirectory struct {
	Users   map[string]map[string]string // tenant -> user -> role
	Devices map[string]map[string]string // tenant -> client -> ad
}

func (d MapDirectory) UserRole(_ context.Context, t, u string) (string, error) {
	return d.Users[t][u], nil
}

func (d MapDirectory) DeviceName(_ context.Context, t, c string) (string, bool, error) {
	n, ok := d.Devices[t][c]
	return n, ok, nil
}
