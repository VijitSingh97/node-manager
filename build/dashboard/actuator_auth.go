package main

import (
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func actuatorRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.EscapedPath()
	return path == "/actuator/health" || path == "/actuator/info"
}

func (a *app) actuatorEnabled(r *http.Request) bool {
	if a.monitor == nil {
		return false
	}
	settings := a.monitor.notificationSettings()
	if r.URL.EscapedPath() == "/actuator/health" {
		return settings.ActuatorHealth
	}
	return settings.ActuatorInfo
}

func (a *app) actuatorEndpoint(handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.actuatorEnabled(r) {
			http.NotFound(w, r)
			return
		}
		handle(w, r)
	}
}

func removeLegacyActuatorCredentials(stateDir string, logger *log.Logger) {
	if stateDir == "" {
		return
	}
	if err := os.Remove(filepath.Join(stateDir, "actuator-credentials.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Print("leftover actuator credentials file could not be deleted")
	}
}

func (a *app) apiActuatorCurl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.hasDashboardPassword() {
		http.Error(w, "set a dashboard password to copy an actuator command", http.StatusForbidden)
		return
	}
	if !a.requireRecentSignIn(w, r) || rejectCredentialCSRF(w, r) {
		return
	}
	var input struct {
		Endpoint string `json:"endpoint"`
		Password string `json:"password"`
	}
	if !decodeStrictBody(w, r, 4096, &input) || input.Endpoint != "health" && input.Endpoint != "info" || input.Password == "" {
		http.Error(w, "invalid actuator request", http.StatusBadRequest)
		return
	}
	settings := a.monitor.notificationSettings()
	if input.Endpoint == "health" && !settings.ActuatorHealth || input.Endpoint == "info" && !settings.ActuatorInfo {
		http.Error(w, "this endpoint is switched off; save the setting first", http.StatusConflict)
		return
	}
	_, status, retry := a.passwordAttempt(r, "admin", input.Password, false, false)
	switch status {
	case http.StatusOK:
		writeJSON(w, map[string]string{"username": "admin"})
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
		http.Error(w, "Too many attempts. Try again shortly.", status)
	default:
		http.Error(w, "dashboard password is incorrect", http.StatusForbidden)
	}
}
