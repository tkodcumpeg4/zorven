package pgstore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Log saklama (retention): ACIK SURUMDE sabit, yapilandirilabilir sure.
// Plana bagli sure ticari katmandadir. Varsayilan 30 gun; ZORVEN_LOG_RETENTION_DAYS
// ile degistirilir (0 veya negatif = temizlik kapali, loglar silinmez).

const (
	retentionBatch = 5000
	// DefaultRetentionDays, ZORVEN_LOG_RETENTION_DAYS tanimsizsa saklama suresi.
	DefaultRetentionDays = 30
)

// RetentionResult, tek bir temizlik kosusunun silme sayilarini tasir.
type RetentionResult struct {
	RequestLogs int64
	UDPStats    int64
}

// RetentionDays, etkin saklama suresini (gun) doner. <=0: temizlik kapali.
func RetentionDays() int {
	v := strings.TrimSpace(os.Getenv("ZORVEN_LOG_RETENTION_DAYS"))
	if v == "" {
		return DefaultRetentionDays
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultRetentionDays
	}
	return n
}

// PruneRetention, RetentionDays()'ten eski request_logs ve udp_stats_minute
// satirlarini (tum kiracilar) partiler halinde siler.
func (s *Store) PruneRetention(ctx context.Context) (RetentionResult, error) {
	return s.pruneRetentionAt(ctx, time.Now().UTC(), RetentionDays())
}

func (s *Store) pruneRetentionAt(ctx context.Context, now time.Time, days int) (RetentionResult, error) {
	var res RetentionResult
	if days <= 0 {
		return res, nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)

	n, err := s.batchDelete(ctx,
		`DELETE FROM request_logs WHERE id IN (
		   SELECT id FROM request_logs WHERE ts < $1 LIMIT $2)`, cutoff)
	res.RequestLogs = n
	if err != nil {
		return res, fmt.Errorf("retention: request_logs: %w", err)
	}
	n, err = s.batchDelete(ctx,
		`DELETE FROM udp_stats_minute WHERE (tunnel_id, minute) IN (
		   SELECT tunnel_id, minute FROM udp_stats_minute WHERE minute < $1 LIMIT $2)`, cutoff)
	res.UDPStats = n
	if err != nil {
		return res, fmt.Errorf("retention: udp_stats_minute: %w", err)
	}
	return res, nil
}

// batchDelete, sorguyu args + son parametre olarak parti boyuyla, etkilenen satir
// retentionBatch'ten azalana kadar tekrarlar.
func (s *Store) batchDelete(ctx context.Context, q string, args ...any) (int64, error) {
	var total int64
	args = append(args, retentionBatch)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		tag, err := s.pool.Exec(ctx, q, args...)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < retentionBatch {
			return total, nil
		}
	}
}
