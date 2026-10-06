package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 5 / HA — tünel replikaları. Bir tünel, birincil client_id'sine ek olarak
// başka istemciler tarafından da servis edilebilir; ingress çevrimiçi üyeler
// arasında round-robin dağıtır.

// tunnelOwnedBy, tünelin verilen kiracıya ait olup olmadığını doğrular.
func (s *Store) tunnelOwnedBy(ctx context.Context, tenantID, tunnelID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id = $1 AND tenant_id = $2)`,
		tunnelID, tenantID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("tunel sahipligi dogrulanamadi: %w", err)
	}
	return ok, nil
}

func (s *Store) AddTunnelReplica(ctx context.Context, tenantID, tunnelID, clientID string) error {
	owns, err := s.tunnelOwnedBy(ctx, tenantID, tunnelID)
	if err != nil {
		return err
	}
	if !owns {
		return store.ErrNotFound
	}
	// İstemci de aynı kiracıya ait olmalı (başka kiracının agent'ı eklenemez).
	var clientOK bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1 AND tenant_id = $2)`,
		clientID, tenantID).Scan(&clientOK); err != nil {
		return fmt.Errorf("istemci sahipligi dogrulanamadi: %w", err)
	}
	if !clientOK {
		return store.ErrNotFound
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO tunnel_replicas (tunnel_id, client_id) VALUES ($1, $2)
		 ON CONFLICT (tunnel_id, client_id) DO NOTHING`,
		tunnelID, clientID)
	if err != nil {
		return fmt.Errorf("replika eklenemedi: %w", err)
	}
	return nil
}

func (s *Store) RemoveTunnelReplica(ctx context.Context, tenantID, tunnelID, clientID string) error {
	owns, err := s.tunnelOwnedBy(ctx, tenantID, tunnelID)
	if err != nil {
		return err
	}
	if !owns {
		return store.ErrNotFound
	}
	_, err = s.pool.Exec(ctx,
		`DELETE FROM tunnel_replicas WHERE tunnel_id = $1 AND client_id = $2`,
		tunnelID, clientID)
	if err != nil {
		return fmt.Errorf("replika kaldirilamadi: %w", err)
	}
	return nil
}

func (s *Store) ListTunnelReplicas(ctx context.Context, tenantID, tunnelID string) ([]string, error) {
	owns, err := s.tunnelOwnedBy(ctx, tenantID, tunnelID)
	if err != nil {
		return nil, err
	}
	if !owns {
		return nil, store.ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`SELECT client_id FROM tunnel_replicas WHERE tunnel_id = $1 ORDER BY created_at`,
		tunnelID)
	if err != nil {
		return nil, fmt.Errorf("replikalar listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
