package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const dashboardCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

type dashboardAuthMethod struct{}

func configureDashboardOrigin(cfg *config, value string) error {
	if value == "" {
		return nil
	}
	origin, err := url.Parse(value)
	if err != nil || value != strings.TrimSpace(value) || strings.ContainsAny(value, "?#") || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.Opaque != "" || strings.HasSuffix(origin.Host, ":") {
		return errors.New("DASHBOARD_PUBLIC_URL must be an HTTPS origin without a path, credentials, query or fragment")
	}
	if _, err := netip.ParseAddr(origin.Hostname()); err != nil && !validSettingsDNSName(strings.ToLower(origin.Hostname())) {
		return errors.New("DASHBOARD_PUBLIC_URL has an invalid hostname")
	}
	if port := origin.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("DASHBOARD_PUBLIC_URL has an invalid port")
		}
	}
	if len(cfg.dashboardPassword) < 12 || len(cfg.dashboardPassword) > 128 || strings.TrimSpace(cfg.dashboardPassword) == "" {
		return errors.New("DASHBOARD_PUBLIC_URL requires a dashboard password of at least 12 characters")
	}
	origin.Host = strings.ToLower(origin.Host)
	origin.Host = strings.TrimSuffix(origin.Host, ":443")
	cfg.dashboardPublicURL = origin.String()
	return nil
}

func (a *app) secureSession(r *http.Request) bool {
	return a.cfg.dashboardPublicURL != "" || r.TLS != nil
}

func (a *app) authCookieName(r *http.Request, suffix string) string {
	if a.secureSession(r) {
		return "__Host-node-manager-" + suffix
	}
	return "node-manager-" + suffix
}

func (a *app) sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(a.authCookieName(r, "session"))
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (a *app) setAuthCookie(w http.ResponseWriter, r *http.Request, suffix, token string, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: a.authCookieName(r, suffix), Value: token, Path: "/",
		Secure: a.secureSession(r), HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(lifetime.Seconds()), Expires: time.Now().Add(lifetime),
	})
}

func (a *app) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	a.setAuthCookie(w, r, "session", "", -time.Second)
}

func (a *app) browserSignIn(r *http.Request) bool {
	if cookie, err := r.Cookie(a.authCookieName(r, "browser")); err == nil && cookie.Value == "1" {
		return true
	}
	for _, header := range []string{"Sec-Fetch-Mode", "Sec-Fetch-Site", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
		if r.Header.Get(header) != "" {
			return true
		}
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html") || strings.Contains(accept, "application/xhtml+xml")
}

func (a *app) useBrowserSignIn(w http.ResponseWriter, r *http.Request) {
	a.setAuthCookie(w, r, "browser", "1", 365*24*time.Hour)
}

func (a *app) expectedOrigin(r *http.Request) string {
	if a.cfg.dashboardPublicURL != "" {
		return a.cfg.dashboardPublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (a *app) sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == a.expectedOrigin(r)
	}
	return true
}

func (a *app) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", dashboardCSP)
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if a.cfg.dashboardPublicURL != "" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if a.cfg.dashboardPublicURL != "" {
			public, _ := url.Parse(a.cfg.dashboardPublicURL)
			_, _, basic := r.BasicAuth()
			internalClient := basic && !a.browserSignIn(r) && privateRemoteAllowed(r.RemoteAddr)
			if !strings.EqualFold(strings.TrimSuffix(r.Host, ":443"), public.Host) && !internalClient {
				http.Error(w, "dashboard host is not configured", http.StatusForbidden)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !a.sameOrigin(r) {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/login" || r.URL.Path == "/logout" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if !a.hasDashboardPassword() {
			http.Error(w, "Dashboard sign-in is not configured", http.StatusServiceUnavailable)
			return
		}
		if a.sessions.valid(a.sessionToken(r), time.Now()) {
			r = r.WithContext(context.WithValue(r.Context(), dashboardAuthMethod{}, "session"))
			next.ServeHTTP(w, r)
			return
		}
		if user, password, ok := r.BasicAuth(); ok && !a.browserSignIn(r) {
			_, status, retry := a.passwordAttempt(r, user, password, false, false)
			if status == http.StatusOK {
				r = r.WithContext(context.WithValue(r.Context(), dashboardAuthMethod{}, "basic"))
				next.ServeHTTP(w, r)
				return
			}
			writeAuthError(w, status, retry)
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		writeAuthError(w, http.StatusUnauthorized, 0)
	})
}

func writeAuthError(w http.ResponseWriter, status int, retry time.Duration) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
		http.Error(w, "Too many sign-in attempts. Try again shortly.", status)
		return
	}
	http.Error(w, "Sign-in required", status)
}

func (a *app) passwordAttempt(r *http.Request, user, password string, issueSession, remember bool) (string, int, time.Duration) {
	now := time.Now()
	remote, identified := a.loginClient(r)
	if identified {
		if allowed, retry := a.loginLimiter.allow(remote, now); !allowed {
			return "", http.StatusTooManyRequests, retry
		}
	}
	select {
	case a.passwordAttempts <- struct{}{}:
		defer func() { <-a.passwordAttempts }()
	default:
		return "", http.StatusTooManyRequests, time.Second
	}
	if allowed, retry := a.passwordWork.allow(time.Now()); !allowed {
		return "", http.StatusTooManyRequests, retry
	}
	auth := a.dashboardAuth
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if user != "admin" || len(password) > 1024 || !auth.enabled || !auth.matchesLocked(password) {
		if identified {
			a.loginLimiter.failure(remote, time.Now())
		}
		return "", http.StatusUnauthorized, 0
	}
	if !issueSession {
		return "", http.StatusOK, 0
	}
	token, err := a.sessions.issue(time.Now(), remember)
	if err != nil {
		return "", http.StatusServiceUnavailable, 0
	}
	return token, http.StatusOK, 0
}

func (a *app) loginClient(r *http.Request) (string, bool) {
	if a.cfg.dashboardPublicURL != "" && privateRemoteAllowed(r.RemoteAddr) {
		if ip, err := netip.ParseAddr(r.Header.Get("X-Node-Manager-Client-IP")); err == nil && ip.Zone() == "" {
			return net.JoinHostPort(ip.String(), "0"), true
		}
	}
	return "", false
}

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	if !a.hasDashboardPassword() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	a.renderLogin(w, r, "", http.StatusOK)
}

func (a *app) renderLogin(w http.ResponseWriter, r *http.Request, message string, status int) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	a.useBrowserSignIn(w, r)
	a.setAuthCookie(w, r, "login", token, 10*time.Minute)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := a.template.ExecuteTemplate(w, "login.html", struct{ CSRF, Error string }{token, message}); err != nil {
		a.log.Print("render sign-in page unavailable")
	}
}

func (a *app) loginPost(w http.ResponseWriter, r *http.Request) {
	if !a.hasDashboardPassword() {
		http.Error(w, "Configure a dashboard password before signing in", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		http.Error(w, "Invalid sign-in request", http.StatusBadRequest)
		return
	}
	csrf := r.PostForm.Get("csrf")
	cookie, err := r.Cookie(a.authCookieName(r, "login"))
	_, validToken := dashboardSessionKey(csrf)
	if err != nil || !validToken || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(csrf)) != 1 {
		http.Error(w, "Sign-in expired. Reload the sign-in page and try again.", http.StatusForbidden)
		return
	}
	remember := r.PostForm.Get("remember") == "1"
	token, status, retry := a.passwordAttempt(r, r.PostForm.Get("username"), r.PostForm.Get("password"), true, remember)
	if status != http.StatusOK {
		message := "Username or password is incorrect."
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
			message = "Too many sign-in attempts. Try again shortly."
		} else if status != http.StatusUnauthorized {
			message = "Sign-in unavailable. Try again shortly."
		}
		a.renderLogin(w, r, message, status)
		return
	}
	a.sessions.revoke(a.sessionToken(r))
	a.useBrowserSignIn(w, r)
	sessionLifetime := dashboardSessionLifetime
	if remember {
		sessionLifetime = dashboardSessionRememberLifetime
	}
	a.setAuthCookie(w, r, "session", token, sessionLifetime)
	a.setAuthCookie(w, r, "login", "", -time.Second)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if rejectCredentialCSRF(w, r) {
		return
	}
	a.sessions.revoke(a.sessionToken(r))
	a.useBrowserSignIn(w, r)
	a.clearSessionCookie(w, r)
	writeJSON(w, map[string]bool{"signed_out": true})
}

func (a *app) requireRecentSignIn(w http.ResponseWriter, r *http.Request) bool {
	if method, _ := r.Context().Value(dashboardAuthMethod{}).(string); method == "basic" || a.sessions.recent(a.sessionToken(r), time.Now()) {
		return true
	}
	http.Error(w, "Sign in again before managing RPC credentials", http.StatusUnauthorized)
	return false
}
