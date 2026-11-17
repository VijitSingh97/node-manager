package main

import (
	"net/http"
	"os"
	"path/filepath"
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

func removeLegacyActuatorCredentials(stateDir string) {
	if stateDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(stateDir, "actuator-credentials.json"))
}
