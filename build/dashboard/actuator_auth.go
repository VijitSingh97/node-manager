package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	actuatorUsername        = "actuator"
	actuatorPasswordBytes   = 32
	actuatorCredentialsFile = "actuator-credentials.json"
)

type actuatorCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c actuatorCredentials) valid() bool {
	raw, err := base64.RawURLEncoding.DecodeString(c.Password)
	return c.Username == actuatorUsername && err == nil && len(raw) >= 16
}

func loadOrCreateActuatorCredentials(stateDir string) (actuatorCredentials, error) {
	path := filepath.Join(stateDir, actuatorCredentialsFile)
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 && info.Size() <= 1024 {
		if b, err := os.ReadFile(path); err == nil {
			var saved actuatorCredentials
			decoder := json.NewDecoder(bytes.NewReader(b))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&saved) == nil && ensureJSONEnd(decoder) == nil && saved.valid() {
				return saved, nil
			}
		}
	}
	raw := make([]byte, actuatorPasswordBytes)
	_, _ = rand.Read(raw)
	created := actuatorCredentials{Username: actuatorUsername, Password: base64.RawURLEncoding.EncodeToString(raw)}
	b, _ := json.Marshal(created)
	return created, writePrivateFileAtomic(path, append(b, '\n'))
}

func actuatorRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.EscapedPath()
	return path == "/actuator/health" || path == "/actuator/info"
}

func (a *app) actuatorEndpoint(enabled func(notificationSettings) bool, handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.monitor == nil || !enabled(a.monitor.notificationSettings()) {
			http.NotFound(w, r)
			return
		}
		now := time.Now()
		remote, identified := a.loginClient(r)
		if identified {
			if allowed, retry := a.actuatorLimiter.allow(remote, now); !allowed {
				writeAuthError(w, http.StatusTooManyRequests, retry)
				return
			}
		}
		if !validBasicAuth(r, a.actuator.Username, a.actuator.Password) {
			if identified {
				a.actuatorLimiter.failure(remote, now)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="node-manager actuator", charset="UTF-8"`)
			writeAuthError(w, http.StatusUnauthorized, 0)
			return
		}
		handle(w, r)
	}
}
