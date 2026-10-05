package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"studbot/internal/bot"
	"studbot/internal/config"
	"studbot/internal/storage"
)

func newTestPanel(t *testing.T, password string, localOnly bool) *Server {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := New(&config.Config{
		AdminPassword:  password,
		AdminOnlyLocal: localOnly,
		DBPath:         filepath.Join(dir, "test.db"),
		UploadDir:      dir,
	}, db, bot.NewProvider())
	s.routes()
	return s
}

func TestPanelSecurityHeaders(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want frame-ancestors 'none'", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("X-Frame-Options must be DENY")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header is missing")
	}
}

func do(s *Server, method, target, remoteAddr string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Requested-With", "panel")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func TestPanelRequiresPassword(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	rec := do(s, http.MethodGet, "/api/panel/logs", "203.0.113.10:5555", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	wrong := &http.Cookie{Name: "panel_auth", Value: "secret"}
	if rec := do(s, http.MethodGet, "/api/panel/logs", "203.0.113.10:5555", wrong); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status with raw password cookie = %d, want 401", rec.Code)
	}

	ok := &http.Cookie{Name: "panel_auth", Value: s.sessionToken()}
	if rec := do(s, http.MethodGet, "/api/panel/logs", "203.0.113.10:5555", ok); rec.Code != http.StatusOK {
		t.Fatalf("status with session cookie = %d, want 200", rec.Code)
	}
}

// ADMIN_ONLY_LOCAL=false must not disable the password (that was the old bug).
func TestPanelNotLocalKeepsPassword(t *testing.T) {
	s := newTestPanel(t, "secret", false)
	if rec := do(s, http.MethodGet, "/api/panel/logs", "8.8.8.8:1000", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a remote unauthenticated request", rec.Code)
	}
}

// Without a configured password the panel is reachable from localhost only.
func TestPanelWithoutPasswordIsLoopbackOnly(t *testing.T) {
	s := newTestPanel(t, "", true)

	if rec := do(s, http.MethodGet, "/api/panel/logs", "127.0.0.1:5555", nil); rec.Code != http.StatusOK {
		t.Fatalf("loopback status = %d, want 200", rec.Code)
	}
	if rec := do(s, http.MethodGet, "/api/panel/logs", "192.168.1.50:5555", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("LAN status = %d, want 403", rec.Code)
	}
	if rec := do(s, http.MethodGet, "/api/panel/logs", "8.8.8.8:1000", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, want 403", rec.Code)
	}
}

func TestPanelLocalOnlyBlocksRemote(t *testing.T) {
	s := newTestPanel(t, "secret", true)
	cookie := &http.Cookie{Name: "panel_auth", Value: s.sessionToken()}

	if rec := do(s, http.MethodGet, "/api/panel/logs", "8.8.8.8:1000", cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, want 403", rec.Code)
	}
	if rec := do(s, http.MethodGet, "/api/panel/logs", "192.168.1.50:5555", cookie); rec.Code != http.StatusOK {
		t.Fatalf("LAN status = %d, want 200", rec.Code)
	}
}

// State-changing endpoints must reject GET, otherwise a cross-site navigation
// could delete content.
func TestPanelMutationsRejectGET(t *testing.T) {
	s := newTestPanel(t, "secret", false)
	cookie := &http.Cookie{Name: "panel_auth", Value: s.sessionToken()}

	targets := []string{
		"/api/panel/delete?kind=book&id=1",
		"/api/panel/clear?what=logs",
		"/api/panel/role?tg_id=1&role=root",
		"/api/panel/broadcast?text=hi",
		"/api/panel/add-book?title=a&subject=b",
		"/api/panel/add-homework?title=a&subject=b",
		"/api/panel/add-event?title=a",
		"/api/panel/poll/close?id=1",
	}
	for _, target := range targets {
		rec := do(s, http.MethodGet, target, "127.0.0.1:5555", cookie)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, want 405", target, rec.Code)
		}
	}
}

func TestPanelStaticAssetsEmbedded(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	cookie := &http.Cookie{Name: "panel_auth", Value: s.sessionToken()}

	rec := do(s, http.MethodGet, "/", "127.0.0.1:5555", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/panel.js") {
		t.Fatalf("GET / = %d, body %q", rec.Code, rec.Body.String())
	}

	rec = do(s, http.MethodGet, "/panel.js", "127.0.0.1:5555", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "api/panel/status") {
		t.Fatalf("GET /panel.js = %d", rec.Code)
	}

	rec = do(s, http.MethodGet, "/panel.css", "127.0.0.1:5555", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /panel.css = %d", rec.Code)
	}
}

func TestPanelShellRequiresLogin(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET / without cookie = %d %q, want 302 to /login", rec.Code, rec.Header().Get("Location"))
	}

	cookie := &http.Cookie{Name: "panel_auth", Value: s.sessionToken()}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/panel.js") {
		t.Fatalf("GET / with cookie = %d", rec.Code)
	}
}

func TestPanelNoInlineHandlers(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	for _, target := range []string{"/", "/panel.js"} {
		rec := do(s, http.MethodGet, target, "127.0.0.1:5555", nil)
		body := rec.Body.String()
		for _, bad := range []string{"onclick=", "onchange=", "javascript:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q, which a strict CSP blocks", target, bad)
			}
		}
	}
}

func TestLoginThrottle(t *testing.T) {
	s := newTestPanel(t, "secret", false)

	for i := 0; i < maxLoginAttempts; i++ {
		s.loginFailed()
	}
	if !s.loginBlocked() {
		t.Fatal("panel must be blocked after repeated failures")
	}
	s.loginSucceeded()
	if s.loginBlocked() {
		t.Fatal("successful login must clear the block")
	}
}
