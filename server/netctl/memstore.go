package netctl

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"time"
)

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// MemStore, bellek ici Store (testler ve kalicilik gerektirmeyen kurulumlar).
type MemStore struct {
	mu     sync.Mutex
	nodes  map[string]NodeRec // tenant|id
	shares map[string]Share
	nets   map[string]netip.Prefix
	rules  map[string]TunRule
	access []AccessRecord
	nextID int64
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

// --- TunStore (W3) ---------------------------------------------------------------

func (s *MemStore) GetNetwork(_ context.Context, tenantID string) (netip.Prefix, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.nets[tenantID]
	return p, ok, nil
}

func (s *MemStore) PutNetwork(_ context.Context, tenantID string, cidr netip.Prefix) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nets == nil {
		s.nets = map[string]netip.Prefix{}
	}
	s.nets[tenantID] = cidr
	return nil
}

func (s *MemStore) ListTunRules(_ context.Context, tenantID string) ([]TunRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TunRule
	for _, r := range s.rules {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *MemStore) PutTunRule(_ context.Context, r TunRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rules == nil {
		s.rules = map[string]TunRule{}
	}
	s.rules[r.TenantID+"|"+r.ID] = r
	return nil
}

func (s *MemStore) DeleteTunRule(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, tenantID+"|"+id)
	return nil
}

var _ TunStore = (*MemStore)(nil)

// --- Z5: erisim kayitlari (AccessStore) ---------------------------------------------

func (s *MemStore) AppendAccess(_ context.Context, tenantID string, recs []AccessRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		s.nextID++
		r.ID, r.TenantID = s.nextID, tenantID
		s.access = append(s.access, r)
	}
	if over := len(s.access) - 5000; over > 0 {
		s.access = append([]AccessRecord(nil), s.access[over:]...)
	}
	return nil
}

func (s *MemStore) ListAccess(_ context.Context, tenantID string, q AccessQuery) ([]AccessRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AccessRecord
	for i := len(s.access) - 1; i >= 0; i-- {
		r := s.access[i]
		if r.TenantID != tenantID {
			continue
		}
		if q.Decision != "" && r.Decision != q.Decision {
			continue
		}
		if q.Resource != "" && r.Resource != q.Resource {
			continue
		}
		if q.Node != "" && r.SrcName != q.Node && r.DstName != q.Node && r.SrcClientID != q.Node && r.DstClientID != q.Node {
			continue
		}
		if !q.Since.IsZero() && r.At.Before(q.Since) {
			continue
		}
		out = append(out, r)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (s *MemStore) PruneAccess(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.access[:0]
	for _, r := range s.access {
		if !r.At.Before(before) {
			keep = append(keep, r)
		}
	}
	s.access = keep
	return nil
}
