package main

import (
	"encoding/base64"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDashboardSessionsLifecycleAndTokenStorage(t *testing.T) {
	sessions := newDashboardSessions()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	seen := make(map[string]bool)
	for range 16 {
		token, err := sessions.issue(now, false)
		raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if err != nil || decodeErr != nil || len(raw) != 32 || len(token) != 43 || seen[token] {
			t.Fatalf("invalid session token: len=%d raw=%d err=%v decode=%v", len(token), len(raw), err, decodeErr)
		}
		seen[token] = true
		key, ok := dashboardSessionKey(token)
		if !ok {
			t.Fatal("issued token rejected")
		}
		if _, stored := sessions.sessions[key]; !stored {
			t.Fatal("session was not stored by token hash")
		}
	}
	var token string
	for token = range seen {
		break
	}
	if !sessions.valid(token, now.Add(29*time.Minute)) {
		t.Fatal("active session expired early")
	}
	sessions.revoke(token)
	if sessions.valid(token, now.Add(29*time.Minute)) {
		t.Fatal("revoked session remained valid")
	}
	sessions.revokeAll()
	if len(sessions.sessions) != 0 {
		t.Fatal("revokeAll retained sessions")
	}
	for _, malformed := range []string{"", "abc", token + "=", token[:42] + "!"} {
		if sessions.valid(malformed, now) {
			t.Fatalf("malformed token accepted: %q", malformed)
		}
		sessions.revoke(malformed)
	}
}

func TestDashboardSessionsIdleAndAbsoluteExpiry(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	sessions := newDashboardSessions()
	idle, _ := sessions.issue(now, false)
	if !sessions.valid(idle, now.Add(dashboardSessionIdle-time.Nanosecond)) || sessions.valid(idle, now.Add(2*dashboardSessionIdle)) {
		t.Fatal("idle expiry boundary is incorrect")
	}
	absolute, _ := sessions.issue(now, false)
	for elapsed := 29 * time.Minute; elapsed < dashboardSessionLifetime; elapsed += 29 * time.Minute {
		if !sessions.valid(absolute, now.Add(elapsed)) {
			t.Fatalf("active session expired at %v", elapsed)
		}
	}
	if sessions.valid(absolute, now.Add(dashboardSessionLifetime)) {
		t.Fatal("session survived absolute lifetime")
	}
}

func TestDashboardSessionsRememberedExpiry(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	sessions := newDashboardSessions()
	remembered, _ := sessions.issue(now, true)
	if !sessions.valid(remembered, now.Add(2*dashboardSessionIdle)) {
		t.Fatal("remembered session hit the idle timeout")
	}
	if !sessions.valid(remembered, now.Add(dashboardSessionRememberLifetime-time.Nanosecond)) {
		t.Fatal("remembered session expired before its lifetime")
	}
	if sessions.valid(remembered, now.Add(dashboardSessionRememberLifetime)) {
		t.Fatal("remembered session survived its lifetime")
	}
}

func TestDashboardSessionsRecentAuthentication(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	sessions := newDashboardSessions()
	token, _ := sessions.issue(now, false)
	if !sessions.recent(token, now.Add(dashboardSessionRecent-time.Nanosecond)) {
		t.Fatal("recent session rejected early")
	}
	if sessions.recent(token, now.Add(dashboardSessionRecent)) {
		t.Fatal("session remained recent at the age limit")
	}
	if !sessions.valid(token, now.Add(dashboardSessionRecent)) {
		t.Fatal("recent-authentication limit expired the session")
	}
	sessions.revoke(token)
	if sessions.recent(token, now) || sessions.recent("malformed", now) {
		t.Fatal("invalid session passed recent authentication")
	}
	idle, _ := sessions.issue(now, false)
	if sessions.recent(idle, now.Add(dashboardSessionIdle)) {
		t.Fatal("idle session passed recent authentication")
	}
}

func TestDashboardSessionsCapacityEvictsLeastRecentlyUsed(t *testing.T) {
	sessions := newDashboardSessions()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	tokens := make([]string, dashboardSessionLimit)
	for i := range tokens {
		tokens[i], _ = sessions.issue(now, false)
	}
	if !sessions.valid(tokens[0], now.Add(time.Second)) {
		t.Fatal("could not refresh session")
	}
	newest, _ := sessions.issue(now.Add(time.Second), false)
	if len(sessions.sessions) != dashboardSessionLimit || !sessions.valid(tokens[0], now.Add(2*time.Second)) || sessions.valid(tokens[1], now.Add(2*time.Second)) || !sessions.valid(newest, now.Add(2*time.Second)) {
		t.Fatal("capacity did not deterministically evict the oldest idle session")
	}
}

func TestDashboardSessionsConcurrentAccess(t *testing.T) {
	sessions := newDashboardSessions()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := range 128 {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			token, err := sessions.issue(now.Add(time.Duration(offset)*time.Millisecond), false)
			if err != nil {
				t.Errorf("issue: %v", err)
				return
			}
			sessions.valid(token, now.Add(time.Second))
			sessions.revoke(token)
		}(i)
	}
	wg.Wait()
	if len(sessions.sessions) > dashboardSessionLimit {
		t.Fatalf("session capacity exceeded: %d", len(sessions.sessions))
	}
}

func TestLoginLimiterPerIPAndCanonicalization(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := range loginFailureLimit {
		remote := "192.0.2.1:1234"
		if i%2 == 1 {
			remote = "[::ffff:192.0.2.1]:5678"
		}
		if ok, retry := limiter.allow(remote, now); !ok || retry != 0 {
			t.Fatalf("failure %d blocked early", i+1)
		}
		limiter.failure(remote, now)
	}
	if ok, retry := limiter.allow("192.0.2.1:9999", now); ok || retry != time.Minute {
		t.Fatalf("per-IP limit not enforced: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow("192.0.2.1:9999", now.Add(time.Minute)); !ok || retry != 0 {
		t.Fatalf("expired failures not pruned: ok=%v retry=%v", ok, retry)
	}
	for range 20 {
		if ok, _ := limiter.allow("192.0.2.2:1234", now); !ok {
			t.Fatal("successful checks consumed the failure budget")
		}
	}
}

func TestLoginLimiterDistinctClientsDoNotBlockUntouchedClient(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 2*loginLimiterKeyLimit; i++ {
		remote := fmt.Sprintf("[2001:db8::%x]:1234", i)
		if ok, _ := limiter.allow(remote, now); !ok {
			t.Fatalf("distinct client %d blocked", i)
		}
		limiter.failure(remote, now)
	}
	if ok, retry := limiter.allow("198.51.100.1:1234", now); !ok || retry != 0 {
		t.Fatalf("untouched client blocked: ok=%v retry=%v", ok, retry)
	}
	if len(limiter.byIP) != loginLimiterKeyLimit {
		t.Fatalf("client buckets exceeded limit: %d", len(limiter.byIP))
	}
}

func TestLoginLimiterFullMapEvictsOldestFailureBucket(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < loginLimiterKeyLimit-1; i++ {
		limiter.byIP[fmt.Sprintf("key-%d", i)] = []time.Time{now.Add(time.Duration(i) * time.Nanosecond)}
	}
	limiter.failure("203.0.113.1:1234", now)
	if len(limiter.byIP) != loginLimiterKeyLimit {
		t.Fatalf("failure bucket count changed: %d", len(limiter.byIP))
	}
	if _, ok := limiter.byIP["key-0"]; ok {
		t.Fatal("oldest failure bucket was not evicted")
	}
	if len(limiter.byIP["203.0.113.1"]) != 1 {
		t.Fatal("new client failure was not recorded")
	}
	for range 100 {
		limiter.failure("malformed", now)
	}
	if len(limiter.byIP) != loginLimiterKeyLimit || len(limiter.byIP[loginOverflowKey]) != loginFailureLimit {
		t.Fatalf("malformed bucket exceeded limits: keys=%d failures=%d", len(limiter.byIP), len(limiter.byIP[loginOverflowKey]))
	}
}

func TestLoginLimiterMalformedAddressesShareOneBucket(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	malformed := []string{"", "192.0.2.1", "bad:port", "[not-an-ip]:1234"}
	for i := range loginFailureLimit {
		remote := malformed[i%len(malformed)]
		if ok, _ := limiter.allow(remote, now); !ok {
			t.Fatalf("malformed failure %d blocked early", i+1)
		}
		limiter.failure(remote, now)
	}
	if ok, retry := limiter.allow("different-invalid", now); ok || retry != time.Minute {
		t.Fatalf("malformed addresses bypassed shared bucket: ok=%v retry=%v", ok, retry)
	}
	if len(limiter.byIP) != 1 {
		t.Fatalf("malformed addresses allocated keys: %d", len(limiter.byIP))
	}
}

func TestPasswordWorkLimiterBurstRefillAndCap(t *testing.T) {
	limiter := newPasswordWorkLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := range passwordWorkBurst {
		if ok, retry := limiter.allow(now); !ok || retry != 0 {
			t.Fatalf("burst reservation %d rejected: retry=%v", i+1, retry)
		}
	}
	if ok, retry := limiter.allow(now); ok || retry != 200*time.Millisecond {
		t.Fatalf("exhausted limiter result: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow(now.Add(200 * time.Millisecond)); !ok || retry != 0 {
		t.Fatalf("refilled token unavailable: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow(now.Add(300 * time.Millisecond)); ok || retry != 100*time.Millisecond {
		t.Fatalf("fractional refill was not retained: ok=%v retry=%v", ok, retry)
	}

	limiter = newPasswordWorkLimiter()
	for range passwordWorkBurst {
		limiter.allow(now)
	}
	for i := range passwordWorkBurst {
		if ok, _ := limiter.allow(now.Add(time.Hour)); !ok {
			t.Fatalf("capped refill reservation %d rejected", i+1)
		}
	}
	if ok, retry := limiter.allow(now.Add(time.Hour)); ok || retry != 200*time.Millisecond {
		t.Fatalf("refill exceeded burst: ok=%v retry=%v", ok, retry)
	}
}

func TestPasswordWorkLimiterDeniedRequestsDoNotDelayRefill(t *testing.T) {
	limiter := newPasswordWorkLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for range passwordWorkBurst {
		limiter.allow(now)
	}
	if ok, retry := limiter.allow(now.Add(50 * time.Millisecond)); ok || retry != 150*time.Millisecond {
		t.Fatalf("first denial: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow(now.Add(150 * time.Millisecond)); ok || retry != 50*time.Millisecond {
		t.Fatalf("denial reset refill: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow(now.Add(200 * time.Millisecond)); !ok || retry != 0 {
		t.Fatalf("denials delayed reservation: ok=%v retry=%v", ok, retry)
	}
}

func TestPasswordWorkLimiterChargesEveryAllowedComparison(t *testing.T) {
	limiter := newPasswordWorkLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := range passwordWorkBurst {
		if ok, _ := limiter.allow(now); !ok {
			t.Fatalf("comparison %d rejected", i+1)
		}
	}
	if ok, _ := limiter.allow(now); ok {
		t.Fatal("allowed comparisons were implicitly refunded")
	}
}

func TestPasswordWorkLimiterConcurrentAndClockRollback(t *testing.T) {
	limiter := newPasswordWorkLimiter()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	allowed := make(chan bool, 4*passwordWorkBurst)
	for range 4 * passwordWorkBurst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := limiter.allow(now)
			allowed <- ok
		}()
	}
	wg.Wait()
	close(allowed)
	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	if count != passwordWorkBurst {
		t.Fatalf("concurrent burst allowed %d comparisons", count)
	}
	if ok, retry := limiter.allow(now.Add(-time.Hour)); ok || retry != 200*time.Millisecond {
		t.Fatalf("backward clock refilled limiter: ok=%v retry=%v", ok, retry)
	}
	if ok, retry := limiter.allow(now.Add(200 * time.Millisecond)); !ok || retry != 0 {
		t.Fatalf("backward clock changed refill origin: ok=%v retry=%v", ok, retry)
	}
}
