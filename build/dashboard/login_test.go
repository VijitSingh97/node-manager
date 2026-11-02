package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDashboardRefusesToStartWithoutSignIn(t *testing.T) {
	root := filepath.Dir(sourceFile(t))
	_, err := newApp(config{bitcoinDisabled: true, stateDir: t.TempDir()}, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "requires a password") {
		t.Fatalf("unprotected dashboard startup = %v", err)
	}
}

func TestDashboardRejectsUnusableBaselinePassword(t *testing.T) {
	root := filepath.Dir(sourceFile(t))
	_, err := newApp(config{bitcoinDisabled: true, dashboardPassword: strings.Repeat("a", 129), stateDir: t.TempDir()}, &rpcClient{}, filepath.Join(root, "index.html"), filepath.Join(root, "static"), log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "128 characters") {
		t.Fatalf("oversized dashboard credential startup = %v", err)
	}
}

func loginRequest(handler http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://nodes.example"+path, strings.NewReader(body))
	req.RemoteAddr = "198.51.100.42:12345"
	if method == http.MethodPost {
		req.Header.Set("Origin", "https://nodes.example")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, suffix string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if strings.HasSuffix(cookie.Name, "-"+suffix) {
			return cookie
		}
	}
	t.Fatalf("response did not set %s cookie", suffix)
	return nil
}

func signIn(t *testing.T, handler http.Handler, password string) *http.Cookie {
	t.Helper()
	page := loginRequest(handler, http.MethodGet, "/login", "")
	if page.Code != http.StatusOK {
		t.Fatalf("login page = %d: %s", page.Code, page.Body.String())
	}
	csrf := responseCookie(t, page, "login")
	form := url.Values{"username": {"admin"}, "password": {password}, "csrf": {csrf.Value}}
	response := loginRequest(handler, http.MethodPost, "/login", form.Encode(), csrf)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("sign in = %d: %s", response.Code, response.Body.String())
	}
	return responseCookie(t, response, "session")
}

func TestSignInPageAndSessionLifecycle(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.dashboardPublicURL = "https://nodes.example"
	handler := a.routes()
	for _, path := range []string{"/api/settings", "/api/watch", "/metrics"} {
		unauthorized := loginRequest(handler, http.MethodGet, path, "")
		if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("WWW-Authenticate") != "" || unauthorized.Header().Get("Location") != "" {
			t.Errorf("unauthorized %s = %d, headers %v", path, unauthorized.Code, unauthorized.Header())
		}
	}
	root := loginRequest(handler, http.MethodGet, "/", "")
	if root.Code != http.StatusFound || root.Header().Get("Location") != "/login" || root.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("root = %d, %v", root.Code, root.Header())
	}
	page := loginRequest(handler, http.MethodGet, "/login", "")
	for _, marker := range []string{`action="/login"`, `name="csrf"`, `autocomplete="current-password"`, `autocomplete="username"`} {
		if !strings.Contains(page.Body.String(), marker) {
			t.Errorf("login page lacks %s", marker)
		}
	}
	csrf := responseCookie(t, page, "login")
	if !strings.Contains(page.Body.String(), csrf.Value) || !csrf.HttpOnly || !csrf.Secure || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatal("login form challenge is not protected")
	}
	session := signIn(t, handler, "dashboard-secret")
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || session.Domain != "" || session.Path != "/" || !strings.HasPrefix(session.Name, "__Host-") {
		t.Fatalf("session cookie = %#v", session)
	}
	if got := loginRequest(handler, http.MethodGet, "/api/settings", "", session); got.Code != http.StatusOK {
		t.Fatalf("signed-in settings = %d: %s", got.Code, got.Body.String())
	}
	logout := httptest.NewRequest(http.MethodPost, "https://nodes.example/logout", nil)
	logout.AddCookie(session)
	logout.Header.Set("Origin", "https://nodes.example")
	logout.Header.Set("X-Requested-With", "fetch")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, logout)
	if response.Code != http.StatusOK || responseCookie(t, response, "session").MaxAge >= 0 {
		t.Fatalf("logout = %d: %s", response.Code, response.Body.String())
	}
	if got := loginRequest(handler, http.MethodGet, "/api/settings", "", session); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", got.Code)
	}
}

func TestSignInRejectsForgeryAndUsesGenericErrors(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	page := loginRequest(handler, http.MethodGet, "/login", "")
	csrf := responseCookie(t, page, "login")
	for _, user := range []string{"admin", "somebody-else"} {
		form := url.Values{"username": {user}, "password": {"wrong"}, "csrf": {csrf.Value}}
		response := loginRequest(handler, http.MethodPost, "/login", form.Encode(), csrf)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "Username or password is incorrect.") || response.Header().Get("WWW-Authenticate") != "" || strings.Contains(response.Body.String(), "value=\"wrong\"") {
			t.Fatalf("wrong login = %d: %s", response.Code, response.Body.String())
		}
	}
	valid := url.Values{"username": {"admin"}, "password": {"dashboard-secret"}, "csrf": {csrf.Value}}
	if got := loginRequest(handler, http.MethodPost, "/login", valid.Encode()); got.Code != http.StatusForbidden {
		t.Fatalf("missing challenge cookie = %d", got.Code)
	}
	for _, origin := range []string{"https://attacker.example", "null", "http://nodes.example"} {
		req := httptest.NewRequest(http.MethodPost, "https://nodes.example/login", strings.NewReader(valid.Encode()))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("origin %q = %d", origin, response.Code)
		}
	}
}

func TestPasswordChangeRevokesAllBrowserSessions(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	first := signIn(t, handler, "dashboard-secret")
	second := signIn(t, handler, "dashboard-secret")
	req := httptest.NewRequest(http.MethodPost, "https://nodes.example/api/settings/password", strings.NewReader(`{"old_password":"dashboard-secret","new_password":"a-new-long-password","repeat_password":"a-new-long-password"}`))
	req.AddCookie(first)
	req.Header.Set("Origin", "https://nodes.example")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("password change = %d: %s", response.Code, response.Body.String())
	}
	for _, cookie := range []*http.Cookie{first, second} {
		if a.sessions.valid(cookie.Value, time.Now()) {
			t.Fatal("password change left an active session")
		}
		if got := loginRequest(handler, http.MethodGet, "/api/settings", "", cookie); got.Code != http.StatusUnauthorized {
			t.Fatalf("old session = %d", got.Code)
		}
	}
	_ = signIn(t, handler, "a-new-long-password")
	cfg := a.cfg
	cfg.dashboardPassword = ""
	restarted, err := newApp(cfg, a.rpc, "index.html", "static", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("saved password did not protect a restarted dashboard: %v", err)
	}
	if got := anonymousRequest(t, restarted.routes(), http.MethodGet, "/api/settings"); got.Code != http.StatusUnauthorized {
		t.Fatalf("saved-password restart exposed settings: %d", got.Code)
	}
	_ = signIn(t, restarted.routes(), "a-new-long-password")
}

func TestCredentialRevealRequiresRecentSignIn(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	token, err := a.sessions.issue(time.Now().Add(-16 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://nodes.example/api/settings/credentials/reveal", strings.NewReader(`{"node":"bitcoin"}`))
	req.AddCookie(&http.Cookie{Name: "__Host-node-manager-session", Value: token})
	req.Header.Set("Origin", "https://nodes.example")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "Sign in again") {
		t.Fatalf("old session revealed RPC credentials: %d: %s", response.Code, response.Body.String())
	}
}

func TestLoginThrottlesFailuresWithoutTrustingForwardedIP(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.dashboardPublicURL = "https://nodes.example"
	handler := a.routes()
	for i := 0; i <= loginFailureLimit; i++ {
		req := httptest.NewRequest(http.MethodGet, "https://nodes.example/api/fees", nil)
		req.RemoteAddr = "172.29.0.25:1234"
		req.SetBasicAuth("admin", "wrong")
		req.Header.Set("X-Node-Manager-Client-IP", "198.51.100.42")
		req.Header.Set("X-Forwarded-For", "10.0.0."+strconv.Itoa(i+1))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		want := http.StatusUnauthorized
		if i == loginFailureLimit {
			want = http.StatusTooManyRequests
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("throttled response lacks retry delay")
			}
		}
		if response.Code != want || response.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("attempt %d = %d, want %d", i, response.Code, want)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://nodes.example/api/fees", nil)
	req.RemoteAddr = "172.29.0.25:1234"
	req.Header.Set("X-Node-Manager-Client-IP", "198.51.100.43")
	req.SetBasicAuth("admin", "dashboard-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("another proxy client was locked out: %d", response.Code)
	}
}

func TestSharedRelayFailuresDoNotLockOutAnotherAdministrator(t *testing.T) {
	_, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	for i := 0; i < loginFailureLimit+1; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/fees", nil)
		req.RemoteAddr = "172.29.0.25:1234"
		req.SetBasicAuth("admin", "wrong")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("shared relay inherited a failure lock: %d", response.Code)
		}
	}
	if got := request(t, handler, http.MethodGet, "/api/fees", "dashboard-secret"); got.Code != http.StatusOK {
		t.Fatalf("shared relay administrator locked out: %d", got.Code)
	}
}

func TestClientIPHeadersRequireProtectedHostProxy(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	for _, test := range []struct{ origin, remote, header string }{
		{"", "172.29.0.25:1234", "198.51.100.2"},
		{"https://nodes.example", "198.51.100.1:1234", "198.51.100.2"},
		{"https://nodes.example", "172.29.0.25:1234", "198.51.100.2, 198.51.100.3"},
		{"https://nodes.example", "172.29.0.25:1234", "localhost"},
	} {
		a.cfg.dashboardPublicURL = test.origin
		req := httptest.NewRequest(http.MethodGet, "/api/fees", nil)
		req.RemoteAddr = test.remote
		req.Header.Set("X-Node-Manager-Client-IP", test.header)
		req.Header.Set("X-Forwarded-For", "198.51.100.4")
		if _, identified := a.loginClient(req); identified {
			t.Errorf("trusted a client-selected identity for %+v", test)
		}
	}
}

func TestAuthenticationSaturationDoesNotBlockExistingSessions(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	session := signIn(t, handler, "dashboard-secret")
	a.passwordWork.mu.Lock()
	a.passwordWork.tokens, a.passwordWork.last = 0, time.Now().Add(time.Minute)
	a.passwordWork.mu.Unlock()
	if got := request(t, handler, http.MethodGet, "/api/fees", "dashboard-secret"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("cached password bypassed work admission: %d", got.Code)
	}
	if got := loginRequest(handler, http.MethodGet, "/api/settings", "", session); got.Code != http.StatusOK {
		t.Fatalf("existing session was denied by password work: %d", got.Code)
	}
}

func TestPublicURLAndOnionCannotBeCombined(t *testing.T) {
	t.Setenv("DASHBOARD_PASSWORD", "dashboard-secret")
	t.Setenv("DASHBOARD_PUBLIC_URL", "https://nodes.example")
	t.Setenv("DASHBOARD_ONION", "1")
	if _, err := envConfig(); err == nil || !strings.Contains(err.Error(), "cannot be enabled together") {
		t.Fatalf("public and onion mode = %v", err)
	}
}

func TestAuthLimitsPasswordWorkAndAllowsSuccessfulMachinePolling(t *testing.T) {
	a, handler := testApp(t, &rpcFixture{}, "dashboard-secret")
	for i := 0; i < 12; i++ {
		if got := request(t, handler, http.MethodGet, "/api/fees", "dashboard-secret"); got.Code != http.StatusOK {
			t.Fatalf("valid poll %d = %d", i, got.Code)
		}
	}
	a.passwordAttempts <- struct{}{}
	a.passwordAttempts <- struct{}{}
	defer func() { <-a.passwordAttempts; <-a.passwordAttempts }()
	if got := request(t, handler, http.MethodGet, "/api/fees", "dashboard-secret"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("unbounded password work: %d", got.Code)
	}
}

func TestProtectedDashboardOriginValidation(t *testing.T) {
	for _, value := range []string{"http://nodes.example", "https://user@nodes.example", "https://nodes.example/", "https://nodes.example?q=a", "https://nodes.example#x", "https://nodes.example#", "https://nodes.example?", "https://nodes.example:70000", " https://nodes.example", "https://nodes.example:", "https://..example", "https://[::::]"} {
		cfg := config{dashboardPassword: "a-strong-password"}
		if configureDashboardOrigin(&cfg, value) == nil {
			t.Errorf("accepted public origin %q", value)
		}
	}
	if configureDashboardOrigin(&config{}, "https://nodes.example") == nil {
		t.Fatal("public origin accepted without a strong password")
	}
	for _, origin := range []string{"https://nodes.example", "https://nodes.example:8443", "https://[2001:db8::1]"} {
		cfg := config{dashboardPassword: "a-strong-password"}
		if err := configureDashboardOrigin(&cfg, origin); err != nil {
			t.Errorf("valid public origin %q: %v", origin, err)
		}
	}
	defaultPort := config{dashboardPassword: "a-strong-password"}
	if err := configureDashboardOrigin(&defaultPort, "https://nodes.example:443"); err != nil || defaultPort.dashboardPublicURL != "https://nodes.example" {
		t.Fatalf("default HTTPS port not normalized: %q, %v", defaultPort.dashboardPublicURL, err)
	}
}

func TestProtectedDashboardHostAndSecurityHeaders(t *testing.T) {
	a, _ := testApp(t, &rpcFixture{}, "dashboard-secret")
	a.cfg.dashboardPublicURL = "https://nodes.example"
	handler := a.routes()
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/login", nil)
	req.Header.Set("X-Forwarded-Host", "nodes.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("spoofed proxy host = %d", response.Code)
	}
	for _, path := range []string{"/login", "/api/settings", "/healthz", "/static/login.css"} {
		response := loginRequest(handler, http.MethodGet, path, "")
		for name, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin", "Strict-Transport-Security": "max-age=31536000"} {
			if response.Header().Get(name) != want {
				t.Errorf("%s missing %s", path, name)
			}
		}
		if !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s missing script restrictions", path)
		}
	}
}
