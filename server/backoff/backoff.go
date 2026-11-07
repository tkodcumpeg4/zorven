// Package backoff, ustel geri cekilme + TAM jitter ile yeniden deneme saglar.
//
// Kullanim: fn gecici hatada normal error, kalici hatada Permanent(err),
// sunucu bekleme istediginde (Retry-After) AfterError doner.
package backoff

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Policy, deneme sayisi ve bekleme sinirlarini belirler.
type Policy struct {
	Attempts int           // toplam deneme (ilk dahil); <1 ise 1
	Base     time.Duration // ilk bekleme tavani (vars. 500ms)
	Max      time.Duration // bekleme tavani (vars. 30s)
}

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent, hatayi "tekrar deneme" olarak isaretler; Do bu hatayi (sarmalsiz) doner.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// IsPermanent, hatanin Permanent ile isaretlenip isaretlenmedigini soyler.
func IsPermanent(err error) bool {
	var p permanent
	return errors.As(err, &p)
}

// AfterError, sonraki denemeden once en az D beklenmesini ister (Retry-After).
type AfterError struct {
	Err error
	D   time.Duration
}

func (a AfterError) Error() string { return a.Err.Error() }
func (a AfterError) Unwrap() error { return a.Err }

// RetryAfter, err'i d kadar bekleme istegiyle sarar (Max ile sinirlanir).
func RetryAfter(err error, d time.Duration) error {
	if err == nil {
		return nil
	}
	return AfterError{Err: err, D: d}
}

// Delay, attempt (1'den baslar) denemesinden sonraki bekleme:
// rand[0, min(Max, Base*2^(attempt-1))].
func (p Policy) Delay(attempt int) time.Duration {
	base, max := p.Base, p.Max
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if max <= 0 {
		max = 30 * time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	ceil := base << min(attempt-1, 20)
	if ceil <= 0 || ceil > max {
		ceil = max
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}

// Sleeper, testlerde gercek bekleme yapmamak icin degistirilebilir.
type Sleeper func(ctx context.Context, d time.Duration) error

// RealSleep, ctx iptaline duyarli gercek bekleme.
func RealSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do, fn'i basarana, Permanent hataya, deneme bitimine veya ctx iptaline kadar calistirir.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error) error {
	return DoWith(ctx, p, RealSleep, fn)
}

// DoWith, Do'nun ozel Sleeper'li halidir.
func DoWith(ctx context.Context, p Policy, sleep Sleeper, fn func(ctx context.Context) error) error {
	n := max(p.Attempts, 1)
	maxWait := p.Max
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}
	var err error
	for i := 1; i <= n; i++ {
		if cerr := ctx.Err(); cerr != nil {
			if err != nil {
				return err
			}
			return cerr
		}
		err = fn(ctx)
		if err == nil {
			return nil
		}
		var pe permanent
		if errors.As(err, &pe) {
			return pe.err
		}
		if i == n {
			break
		}
		wait := p.Delay(i)
		var ae AfterError
		if errors.As(err, &ae) {
			wait = min(ae.D, maxWait)
		}
		if serr := sleep(ctx, wait); serr != nil {
			return err
		}
	}
	return err
}
