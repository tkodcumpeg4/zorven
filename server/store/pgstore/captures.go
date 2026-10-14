package pgstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// Kalici istek yakalama deposu (reqlog.CaptureBackend). Sifreleme/sikistirma
// reqlog.CaptureCodec'te yapilir; burasi yalniz sifreli bloklari saklar.

var _ reqlog.CaptureBackend = (*Store)(nil)

// jsonBlob, sifreli baytlari jsonb uyumlu JSON metnine (base64 string) cevirir.
func jsonBlob(b []byte) string {
	j, _ := json.Marshal(base64.StdEncoding.EncodeToString(b))
	return string(j)
}

func unJSONBlob(raw []byte) ([]byte, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(s)
}

const insertCaptureSQL = `INSERT INTO request_captures
   (id, tenant_id, tunnel_id, ts, meta, req_headers, resp_headers, req_body, resp_body,
    enc_key_version, compressed, req_truncated, resp_truncated, req_size, resp_size, stored_bytes)
 VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::jsonb,$8,$9,$10,$11,$12,$13,$14,$15,$16)
 ON CONFLICT (id) DO NOTHING`

// PutCaptures, sifreli yakalamalari tek batch'te yazar.
func (s *Store) PutCaptures(ctx context.Context, recs []*reqlog.CaptureRecord) error {
	if len(recs) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, r := range recs {
		b.Queue(insertCaptureSQL, r.ID, pgText(r.TenantID), pgText(r.TunnelID), r.TS,
			jsonBlob(r.Meta), jsonBlob(r.ReqHeaders), jsonBlob(r.RespHeaders), r.ReqBody, r.RespBody,
			r.EncKeyVersion, r.Compressed, r.ReqTruncated, r.RespTruncated, r.ReqSize, r.RespSize, r.StoredBytes)
	}
	return s.execBatch(ctx, b)
}

// GetCapture, tek yakalamayi doner; kiraci uyusmazsa/yoksa ErrCaptureNotFound.
func (s *Store) GetCapture(ctx context.Context, tenantID, id string) (*reqlog.CaptureRecord, error) {
	r := &reqlog.CaptureRecord{}
	var meta, rh, sh []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, tunnel_id, ts, meta, req_headers, resp_headers, req_body, resp_body,
		        enc_key_version, compressed, req_truncated, resp_truncated, req_size, resp_size, stored_bytes
		   FROM request_captures WHERE id = $1 AND ($2 = '' OR tenant_id = $2)`, id, tenantID).
		Scan(&r.ID, &r.TenantID, &r.TunnelID, &r.TS, &meta, &rh, &sh, &r.ReqBody, &r.RespBody,
			&r.EncKeyVersion, &r.Compressed, &r.ReqTruncated, &r.RespTruncated, &r.ReqSize, &r.RespSize, &r.StoredBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, reqlog.ErrCaptureNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.Meta, err = unJSONBlob(meta); err != nil {
		return nil, err
	}
	if r.ReqHeaders, err = unJSONBlob(rh); err != nil {
		return nil, err
	}
	if r.RespHeaders, err = unJSONBlob(sh); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteTenantCaptures, kiracinin tum yakalamalarini siler.
func (s *Store) DeleteTenantCaptures(ctx context.Context, tenantID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM request_captures WHERE tenant_id = $1`, tenantID)
	return err
}

// CaptureUsage, kiraci basina saklanan bayt toplamini doner.
func (s *Store) CaptureUsage(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT tenant_id, COALESCE(SUM(stored_bytes),0)::bigint FROM request_captures GROUP BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var t string
		var n int64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// MoveCaptureTablespace, request_captures tablosunu (TOAST dahil) ve
// indekslerini verilen tablespace'e tasir. Tablespace yoksa hata doner
// (cagiran uyari loglayip devam eder). Zaten oradaysa islem yapmaz.
func (s *Store) MoveCaptureTablespace(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tablespace WHERE spcname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("tablespace %q yok (CREATE TABLESPACE ile once olusturun)", name)
	}
	q := pgx.Identifier{name}.Sanitize()
	var inSpace bool
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT spcname FROM pg_tablespace WHERE oid = c.reltablespace), '') = $1
		   FROM pg_class c WHERE c.oid = 'request_captures'::regclass`, name).Scan(&inSpace); err != nil {
		return err
	}
	if !inSpace {
		if _, err := s.pool.Exec(ctx, `ALTER TABLE request_captures SET TABLESPACE `+q); err != nil {
			return err
		}
		slog.Info("request_captures tablosu tablespace'e tasindi", "tablespace", name)
	}
	for _, idx := range []string{"request_captures_pkey", "idx_request_captures_tenant_ts"} {
		var idxIn bool
		err := s.pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT spcname FROM pg_tablespace WHERE oid = c.reltablespace), '') = $1
			   FROM pg_class c WHERE c.oid = $2::text::regclass`, name, idx).Scan(&idxIn)
		if err != nil {
			return err
		}
		if !idxIn {
			if _, err := s.pool.Exec(ctx, `ALTER INDEX `+pgx.Identifier{idx}.Sanitize()+` SET TABLESPACE `+q); err != nil {
				return err
			}
		}
	}
	return nil
}
