package backoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rec struct{ waits []time.Duration }

func (r *rec) sleep(_ context.Context, d time.Duration) error {
	r.waits = append(r.waits, d)
	return nil
}

var errX = errors.New("x")

func TestDoSucceedsAfterRetries(t *testing.T) {
	r := &rec{}
	calls := 0
	err := DoWith(context.Background(), Policy{Attempts: 5, Base: time.Second, Max: 4 * time.Second}, r.sleep,
		func(context.Context) error {
			calls++
			if calls < 3 {
				return errX
			}
			return nil
		})
	if err != nil || calls != 3 || len(r.waits) != 2 {
		t.Fatalf("err=%v calls=%d waits=%v", err, calls, r.waits)
	}
}

func TestDoExhaustsAttempts(t *testing.T) {
	r := &rec{}
	calls := 0
	err := DoWith(context.Background(), Policy{Attempts: 4, Base: time.Second}, r.sleep,
		func(context.Context) error { calls++; return errX })
	if !errors.Is(err, errX) || calls != 4 || len(r.waits) != 3 {
		t.Fatalf("err=%v calls=%d waits=%d", err, calls, len(r.waits))
	}
}

func TestPermanentStops(t *testing.T) {
	r := &rec{}
	calls := 0
	err := DoWith(context.Background(), Policy{Attempts: 5}, r.sleep,
		func(context.Context) error { calls++; return Permanent(errX) })
	if err != errX || calls != 1 || len(r.waits) != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if IsPermanent(errX) || !IsPermanent(Permanent(errX)) {
		t.Fatal("IsPermanent")
	}
}

func TestDelayFullJitterBounds(t *testing.T) {
	p := Policy{Base: time.Second, Max: 8 * time.Second}
	ceils := []time.Duration{1, 2, 4, 8, 8, 8}
	for i, c := range ceils {
		for k := 0; k < 200; k++ {
			d := p.Delay(i + 1)
			if d < 0 || d > c*time.Second {
				t.Fatalf("attempt %d: %v > %v", i+1, d, c*time.Second)
			}
		}
	}
	if d := p.Delay(1000); d > 8*time.Second {
		t.Fatal("overflow")
	}
}

func TestRetryAfterHonouredAndCapped(t *testing.T) {
	r := &rec{}
	calls := 0
	_ = DoWith(context.Background(), Policy{Attempts: 3, Base: time.Millisecond, Max: 10 * time.Second}, r.sleep,
		func(context.Context) error {
			calls++
			if calls == 1 {
				return RetryAfter(errX, 3*time.Second)
			}
			return RetryAfter(errX, time.Hour)
		})
	if len(r.waits) != 2 || r.waits[0] != 3*time.Second || r.waits[1] != 10*time.Second {
		t.Fatalf("waits=%v", r.waits)
	}
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := DoWith(ctx, Policy{Attempts: 5}, func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}, func(context.Context) error { calls++; return errX })
	if calls != 1 || !errors.Is(err, errX) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	// Onceden iptal: fn hic cagrilmaz.
	calls = 0
	err = Do(ctx, Policy{Attempts: 3}, func(context.Context) error { calls++; return nil })
	if calls != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRealSleepCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if RealSleep(ctx, time.Hour) == nil {
		t.Fatal("iptal bekleniyordu")
	}
}
