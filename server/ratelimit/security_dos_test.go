package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestSecurity_RateLimit_BurstAndDenialOfService(t *testing.T) {
	// 10 req/s, burst 20
	lim := New(10, 20)

	// Consume entire burst
	allowed := 0
	for i := 0; i < 20; i++ {
		if lim.Allow("attacker_ip") {
			allowed++
		}
	}
	if allowed != 20 {
		t.Fatalf("expected 20 allowed requests in burst, got %d", allowed)
	}

	// 21st request MUST be rejected (DoS prevention)
	if lim.Allow("attacker_ip") {
		t.Fatalf("SECURITY FLAW: rate limiter permitted requests beyond configured burst limit!")
	}

	// Independent IP should still be allowed (No cross-IP denial of service)
	if !lim.Allow("innocent_ip") {
		t.Fatalf("CROSS-IP BLOCKING: innocent IP was blocked due to attacker traffic!")
	}
}

func TestSecurity_RateLimit_ConcurrentStress(t *testing.T) {
	lim := New(50, 50)
	var wg sync.WaitGroup
	workers := 50
	reqsPerWorker := 20

	var totalAllowed, totalBlocked sync.Mutex
	allowedCount := 0
	blockedCount := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < reqsPerWorker; i++ {
				if lim.Allow("stress_key") {
					totalAllowed.Lock()
					allowedCount++
					totalAllowed.Unlock()
				} else {
					totalBlocked.Lock()
					blockedCount++
					totalBlocked.Unlock()
				}
				time.Sleep(1 * time.Millisecond)
			}
		}(w)
	}
	wg.Wait()

	if allowedCount == 0 {
		t.Errorf("expected some allowed requests")
	}
	if blockedCount == 0 {
		t.Errorf("expected rate limiter to block excess requests under concurrent flood")
	}
}
