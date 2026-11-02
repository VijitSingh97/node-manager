package main

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

const dashboardPasswordIterations = 210000

type savedDashboardAuth struct {
	Version    int    `json:"version"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
}

type dashboardAuth struct {
	mu       sync.RWMutex
	path     string
	enabled  bool
	saved    savedDashboardAuth
	cached   [32]byte
	hasCache bool
}

func newDashboardAuth(stateDir, baseline string) (*dashboardAuth, error) {
	if len(baseline) > 128 {
		return nil, errors.New("dashboard password must not exceed 128 characters")
	}
	auth := &dashboardAuth{path: filepath.Join(stateDir, "dashboard_auth.json"), enabled: baseline != "", cached: sha256.Sum256([]byte(baseline)), hasCache: true}
	info, err := os.Lstat(auth.path)
	if os.IsNotExist(err) {
		return auth, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("dashboard password storage unavailable")
	}
	b, err := os.ReadFile(auth.path)
	if err != nil {
		return nil, errors.New("dashboard password storage unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var saved savedDashboardAuth
	if decoder.Decode(&saved) != nil || ensureJSONEnd(decoder) != nil || saved.Version != 1 || saved.Iterations != dashboardPasswordIterations || !lowerHex(saved.Salt, 32) || !lowerHex(saved.Hash, 64) {
		return nil, errors.New("dashboard password storage unavailable")
	}
	auth.saved, auth.enabled, auth.hasCache = saved, true, false
	return auth, nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (auth *dashboardAuth) matchesLocked(password string) bool {
	digest := sha256.Sum256([]byte(password))
	if auth.hasCache {
		return subtle.ConstantTimeCompare(digest[:], auth.cached[:]) == 1
	}
	salt, _ := hex.DecodeString(auth.saved.Salt)
	expected, _ := hex.DecodeString(auth.saved.Hash)
	derived, err := pbkdf2.Key(sha256.New, password, salt, dashboardPasswordIterations, 32)
	if err != nil || subtle.ConstantTimeCompare(derived, expected) != 1 {
		return false
	}
	auth.cached, auth.hasCache = digest, true
	return true
}

func (auth *dashboardAuth) matches(password string) bool {
	if len(password) > 1024 {
		return false
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	return auth.matchesLocked(password)
}

func (a *app) hasDashboardPassword() bool {
	if a.dashboardAuth == nil {
		return a.cfg.dashboardPassword != ""
	}
	a.dashboardAuth.mu.RLock()
	defer a.dashboardAuth.mu.RUnlock()
	return a.dashboardAuth.enabled
}

func (a *app) dashboardPasswordMatches(password string) bool {
	if a.dashboardAuth != nil {
		return a.dashboardAuth.matches(password)
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(a.cfg.dashboardPassword)) == 1
}

func rejectCredentialCSRF(w http.ResponseWriter, r *http.Request) bool {
	if rejectCSRF(w, r) {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.User != nil || u.Host != r.Host || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return true
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return true
	}
	return false
}

func (a *app) apiDashboardPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if rejectCredentialCSRF(w, r) || a.rejectUnsafeSettingsHost(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Old    string `json:"old_password"`
		New    string `json:"new_password"`
		Repeat string `json:"repeat_password"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil || len(input.New) < 12 || len(input.New) > 128 || strings.IndexFunc(input.New, unicode.IsControl) >= 0 || input.New != input.Repeat {
		http.Error(w, "use matching passwords of 12 to 128 characters without control characters", http.StatusBadRequest)
		return
	}
	auth := a.dashboardAuth
	if auth == nil {
		http.Error(w, "dashboard password storage unavailable", http.StatusServiceUnavailable)
		return
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.enabled && !auth.matchesLocked(input.Old) || !auth.enabled && input.Old != "" {
		http.Error(w, "current password is incorrect", http.StatusForbidden)
		return
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		http.Error(w, "password change unavailable", http.StatusServiceUnavailable)
		return
	}
	hash, err := pbkdf2.Key(sha256.New, input.New, salt, dashboardPasswordIterations, 32)
	if err != nil {
		http.Error(w, "password change unavailable", http.StatusServiceUnavailable)
		return
	}
	saved := savedDashboardAuth{Version: 1, Iterations: dashboardPasswordIterations, Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(hash)}
	b, _ := json.Marshal(saved)
	if writePrivateFileAtomic(auth.path, append(b, '\n')) != nil {
		http.Error(w, "password change could not be saved", http.StatusServiceUnavailable)
		return
	}
	auth.saved, auth.enabled, auth.cached, auth.hasCache = saved, true, sha256.Sum256([]byte(input.New)), true
	a.sessions.revokeAll()
	a.clearSessionCookie(w, r)
	writeJSON(w, map[string]bool{"changed": true})
}
