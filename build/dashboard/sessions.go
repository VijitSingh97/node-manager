package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const (
	dashboardSessionLimit            = 64
	dashboardSessionIdle             = 30 * time.Minute
	dashboardSessionLifetime         = 24 * time.Hour
	dashboardSessionRememberLifetime = 30 * 24 * time.Hour
	dashboardSessionRecent           = 15 * time.Minute
	loginFailureLimit                = 10
	loginFailureWindow               = time.Minute
	loginLimiterKeyLimit             = 1024
	loginOverflowKey                 = "*"
	passwordWorkBurst                = 32
	passwordWorkRate                 = 5
)

type dashboardSession struct {
	issued   time.Time
	lastSeen time.Time
	order    uint64
	remember bool
}

type dashboardSessions struct {
	mu       sync.Mutex
	sessions map[[32]byte]dashboardSession
	next     uint64
	path     string
	binding  string
}

type savedDashboardSessions struct {
	Version  int                     `json:"version"`
	Binding  string                  `json:"binding"`
	Sessions []savedDashboardSession `json:"sessions"`
}

type savedDashboardSession struct {
	Key    string `json:"key"`
	Issued int64  `json:"issued"`
}

func newDashboardSessions() *dashboardSessions {
	return &dashboardSessions{sessions: make(map[[32]byte]dashboardSession)}
}

func (sessions *dashboardSessions) persist(path, binding string, now time.Time) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.path, sessions.binding = path, binding
	saved, ok := readSavedDashboardSessions(path)
	if !ok || saved.Binding != binding {
		return
	}
	for _, item := range saved.Sessions {
		raw, _ := hex.DecodeString(item.Key)
		issued := time.Unix(item.Issued, 0)
		session := dashboardSession{issued: issued, lastSeen: issued, remember: true}
		if issued.After(now) || sessionExpired(session, now) {
			continue
		}
		sessions.next++
		session.order = sessions.next
		sessions.sessions[[32]byte(raw)] = session
	}
}

func readSavedDashboardSessions(path string) (savedDashboardSessions, bool) {
	var saved savedDashboardSessions
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0o077 != 0 {
		return saved, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return saved, false
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || ensureJSONEnd(decoder) != nil || saved.Version != 1 || !lowerHex(saved.Binding, 64) || len(saved.Sessions) > dashboardSessionLimit {
		return saved, false
	}
	for _, item := range saved.Sessions {
		if !lowerHex(item.Key, 64) || item.Issued <= 0 {
			return saved, false
		}
	}
	return saved, true
}

// A failed save removes the file so a revoked session can never come back after a restart.
func (sessions *dashboardSessions) saveLocked() {
	if sessions.path == "" {
		return
	}
	saved := savedDashboardSessions{Version: 1, Binding: sessions.binding, Sessions: []savedDashboardSession{}}
	for key, session := range sessions.sessions {
		if session.remember {
			saved.Sessions = append(saved.Sessions, savedDashboardSession{Key: hex.EncodeToString(key[:]), Issued: session.issued.Unix()})
		}
	}
	b, err := json.Marshal(saved)
	if err != nil || writePrivateFileAtomic(sessions.path, append(b, '\n')) != nil {
		_ = os.Remove(sessions.path)
	}
}

func dashboardSessionKey(token string) ([32]byte, bool) {
	var zero [32]byte
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return zero, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return zero, false
	}
	return sha256.Sum256([]byte(token)), true
}

func sessionExpired(session dashboardSession, now time.Time) bool {
	if session.remember {
		return !now.Before(session.issued.Add(dashboardSessionRememberLifetime))
	}
	return !now.Before(session.lastSeen.Add(dashboardSessionIdle)) || !now.Before(session.issued.Add(dashboardSessionLifetime))
}

func (sessions *dashboardSessions) pruneLocked(now time.Time) {
	for key, session := range sessions.sessions {
		if sessionExpired(session, now) {
			delete(sessions.sessions, key)
		}
	}
}

func (sessions *dashboardSessions) issue(now time.Time, remember bool) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	key := sha256.Sum256([]byte(token))

	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.pruneLocked(now)
	if len(sessions.sessions) >= dashboardSessionLimit {
		var oldestKey [32]byte
		var oldest dashboardSession
		first := true
		for candidateKey, candidate := range sessions.sessions {
			if first || candidate.lastSeen.Before(oldest.lastSeen) || candidate.lastSeen.Equal(oldest.lastSeen) && candidate.order < oldest.order {
				oldestKey, oldest, first = candidateKey, candidate, false
			}
		}
		delete(sessions.sessions, oldestKey)
	}
	sessions.next++
	sessions.sessions[key] = dashboardSession{issued: now, lastSeen: now, order: sessions.next, remember: remember}
	sessions.saveLocked()
	return token, nil
}

func (sessions *dashboardSessions) valid(token string, now time.Time) bool {
	key, ok := dashboardSessionKey(token)
	if !ok {
		return false
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.pruneLocked(now)
	session, ok := sessions.sessions[key]
	if !ok {
		return false
	}
	if now.After(session.lastSeen) {
		session.lastSeen = now
		sessions.sessions[key] = session
	}
	return true
}

func (sessions *dashboardSessions) recent(token string, now time.Time) bool {
	key, ok := dashboardSessionKey(token)
	if !ok {
		return false
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.pruneLocked(now)
	session, ok := sessions.sessions[key]
	return ok && now.Before(session.issued.Add(dashboardSessionRecent))
}

func (sessions *dashboardSessions) revoke(token string) {
	key, ok := dashboardSessionKey(token)
	if !ok {
		return
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if session, ok := sessions.sessions[key]; ok {
		delete(sessions.sessions, key)
		if session.remember {
			sessions.saveLocked()
		}
	}
}

func (sessions *dashboardSessions) revokeAll(binding string) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	clear(sessions.sessions)
	sessions.binding = binding
	sessions.saveLocked()
}

type loginLimiter struct {
	mu   sync.Mutex
	byIP map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{byIP: map[string][]time.Time{loginOverflowKey: nil}}
}

func loginLimiterIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return loginOverflowKey
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return loginOverflowKey
	}
	return addr.WithZone("").Unmap().String()
}

func pruneAttempts(attempts []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-loginFailureWindow)
	first := 0
	for first < len(attempts) && !attempts[first].After(cutoff) {
		first++
	}
	return attempts[first:]
}

func (limiter *loginLimiter) pruneLocked(now time.Time) {
	for key, attempts := range limiter.byIP {
		attempts = pruneAttempts(attempts, now)
		if len(attempts) == 0 && key != loginOverflowKey {
			delete(limiter.byIP, key)
		} else {
			limiter.byIP[key] = attempts
		}
	}
}

func (limiter *loginLimiter) failureKeyLocked(remoteAddr string) string {
	key := loginLimiterIP(remoteAddr)
	if key == loginOverflowKey {
		return key
	}
	if _, ok := limiter.byIP[key]; ok || len(limiter.byIP) < loginLimiterKeyLimit {
		return key
	}
	oldestKey := ""
	var oldest time.Time
	for candidate, attempts := range limiter.byIP {
		if candidate == loginOverflowKey {
			continue
		}
		last := attempts[len(attempts)-1]
		if oldestKey == "" || last.Before(oldest) || last.Equal(oldest) && candidate < oldestKey {
			oldestKey, oldest = candidate, last
		}
	}
	delete(limiter.byIP, oldestKey)
	return key
}

func (limiter *loginLimiter) allow(remoteAddr string, now time.Time) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.pruneLocked(now)
	key := loginLimiterIP(remoteAddr)
	if attempts := limiter.byIP[key]; len(attempts) >= loginFailureLimit {
		return false, attempts[0].Add(loginFailureWindow).Sub(now)
	}
	return true, 0
}

func (limiter *loginLimiter) failure(remoteAddr string, now time.Time) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.pruneLocked(now)
	key := limiter.failureKeyLocked(remoteAddr)
	attempts := limiter.byIP[key]
	if len(attempts) < loginFailureLimit {
		limiter.byIP[key] = append(attempts, now)
	}
}

type passwordWorkLimiter struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newPasswordWorkLimiter() *passwordWorkLimiter {
	return &passwordWorkLimiter{tokens: passwordWorkBurst}
}

func (limiter *passwordWorkLimiter) allow(now time.Time) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	available := limiter.tokens
	if !limiter.last.IsZero() && now.After(limiter.last) {
		available = min(passwordWorkBurst, available+now.Sub(limiter.last).Seconds()*passwordWorkRate)
	}
	if available < 1 {
		retry := math.Ceil((1 - available) / passwordWorkRate * float64(time.Second))
		return false, time.Duration(retry)
	}
	limiter.tokens = available - 1
	if limiter.last.IsZero() || now.After(limiter.last) {
		limiter.last = now
	}
	return true, 0
}
