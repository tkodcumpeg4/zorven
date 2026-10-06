package pgstore

// FAZ 3 / F15 — Uzaktan ajan yapilandirmasi.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// GetDeviceConfig, cihazin kayitli ayarlarini doner.
// Kayit yoksa (nil, nil) doner: "ayar yok" bir hata DEGILDIR.
func (s *Store) GetDeviceConfig(ctx context.Context, tenantID, clientID string) (*protocol.AgentSettings, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT config FROM device_config WHERE client_id = $1 AND tenant_id = $2`,
		clientID, tenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out protocol.AgentSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		// Bozuk kayit, ayarsiz davranmaktan kotu degil: ajan varsayilanlariyla
		// calissin, panelde de bos gorunsun ki kullanici yeniden kaydetsin.
		return nil, nil
	}
	return &out, nil
}

// GetDeviceConfigByClient, KIRACIDAN BAGIMSIZDIR. El sikismasinda kullanilir:
// o anda istemcinin kiracisi zaten token'dan dogrulanmistir.
func (s *Store) GetDeviceConfigByClient(ctx context.Context, clientID string) (*protocol.AgentSettings, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT config FROM device_config WHERE client_id = $1`, clientID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out protocol.AgentSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil
	}
	return &out, nil
}

// SetDeviceConfig, ayarlari yazar (upsert).
func (s *Store) SetDeviceConfig(ctx context.Context, tenantID, clientID string, cfg protocol.AgentSettings) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cihaz ayarlari kodlanamadi: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO device_config (client_id, tenant_id, config, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (client_id) DO UPDATE SET config = EXCLUDED.config, updated_at = now()`,
		clientID, tenantID, raw)
	if err != nil {
		return fmt.Errorf("cihaz ayarlari kaydedilemedi: %w", err)
	}
	return nil
}
