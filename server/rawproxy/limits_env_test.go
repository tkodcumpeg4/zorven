package rawproxy

import (
	"testing"
	"time"
)

func TestConnLimitsEnv(t *testing.T) {
	env := map[string]string{
		"ZORVEN_RAW_TCP_MAX_CONNS":        "abc",      // gecersiz -> varsayilan
		"ZORVEN_RAW_TCP_MAX_CONNS_TUNNEL": "0",        // 0 = sinirsiz korunur
		"ZORVEN_RAW_TCP_MAX_CONNS_IP":     "99999999", // ust sinir disi -> varsayilan
		"ZORVEN_RAW_TCP_IDLE_SEC":         "-5",       // negatif -> varsayilan
	}
	l := newConnLimitsFrom(func(k string) string { return env[k] })
	if l.max != defMaxConns || l.perTunnel != 0 || l.perIP != defMaxConnsIP || l.idle != defIdle {
		t.Fatalf("beklenmeyen sinirlar: max=%d tunel=%d ip=%d idle=%s", l.max, l.perTunnel, l.perIP, l.idle)
	}
	l = newConnLimitsFrom(func(k string) string {
		return map[string]string{"ZORVEN_RAW_TCP_MAX_CONNS": "100", "ZORVEN_RAW_TCP_IDLE_SEC": "60"}[k]
	})
	if l.max != 100 || l.idle != time.Minute {
		t.Fatalf("gecerli degerler okunmadi: max=%d idle=%s", l.max, l.idle)
	}
}
