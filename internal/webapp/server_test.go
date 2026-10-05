package webapp

import (
	"bytes"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"studbot/internal/bot"
	"studbot/internal/config"
	"studbot/internal/storage"
)

const testToken = "5768337691:AAH5YkoiEuPk8-FZa32hStHTqXiLPtAEhx8"

// The example from the official Telegram documentation. It proves that the
// data_check_string and the key derivation are implemented correctly.
func TestBuildDataCheckStringMatchesTelegramDocs(t *testing.T) {
	initData := "query_id=AAHdF6IQAAAAAN0XohDhrOrc" +
		"&user=%7B%22id%22%3A279058397%2C%22first_name%22%3A%22Vladislav%22%2C%22last_name%22%3A%22Kibenko%22%2C%22username%22%3A%22vdkfrost%22%2C%22language_code%22%3A%22ru%22%2C%22is_premium%22%3Atrue%7D" +
		"&auth_date=1662771648" +
		"&hash=c501b71e775f74ce10e377dea85a7ea24ecd640b223ea86dfe453e0eaed2e2b2"

	vals, err := url.ParseQuery(initData)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	secretKey := hmacSHA256([]byte("WebAppData"), []byte(testToken))
	got := hex.EncodeToString(hmacSHA256(secretKey, []byte(buildDataCheckString(vals))))

	if want := vals.Get("hash"); got != want {
		t.Fatalf("hash mismatch: got %s, want %s\ncheck string: %q", got, want, buildDataCheckString(vals))
	}
	if strings.Contains(buildDataCheckString(vals), "hash=") {
		t.Error("data_check_string must not contain the hash field")
	}
}

func newTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := New(&config.Config{
		Token:      testToken,
		UploadDir:  dir,
		ListenAddr: ":0",
	}, db, bot.NewProvider())
	return s, db
}

// serve goes through the real handler chain (security headers included).
func serve(s *Server, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t)
	rec := serve(s, http.MethodGet, "/")

	h := rec.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self' https://telegram.org", "frame-ancestors"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q does not contain %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline'; script-src") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("scripts must not allow inline execution")
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header is missing")
	}
	if h.Get("Referrer-Policy") != "no-referrer" {
		t.Error("referrer policy is missing")
	}
}

func TestStaticAssetsEmbedded(t *testing.T) {
	s, _ := newTestServer(t)

	rec := serve(s, http.MethodGet, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/app.js") {
		t.Fatalf("GET / = %d, body %q", rec.Code, rec.Body.String())
	}

	rec = serve(s, http.MethodGet, "/app.js")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "X-Telegram-Init-Data") {
		t.Fatalf("GET /app.js = %d, body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("app.js content type = %q", ct)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /style.css = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404", rec.Code)
	}
}

func TestNoInlineHandlersInAssets(t *testing.T) {
	s, _ := newTestServer(t)

	for _, target := range []string{"/", "/app.js"} {
		rec := serve(s, http.MethodGet, target)
		body := rec.Body.String()
		for _, bad := range []string{"onclick=", "onchange=", "onload=", "javascript:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q, which a strict CSP blocks", target, bad)
			}
		}
	}
}

func TestFileSignature(t *testing.T) {
	s, _ := newTestServer(t)
	exp := time.Now().Add(time.Minute).Unix()

	sig := s.fileSignature(42, exp)
	if !s.validFileSignature(42, exp, sig) {
		t.Error("fresh signature must be valid")
	}
	if s.validFileSignature(43, exp, sig) {
		t.Error("signature must not validate for another book")
	}
	if s.validFileSignature(42, exp+3600, sig) {
		t.Error("signature must not validate for another expiry")
	}
	past := time.Now().Add(-time.Minute).Unix()
	if s.validFileSignature(42, past, s.fileSignature(42, past)) {
		t.Error("expired signature must be rejected")
	}
	if s.validFileSignature(42, exp, "") {
		t.Error("empty signature must be rejected")
	}
}

func TestFileEndpointRequiresSignature(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/file?book_id=1", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content type = %q, want JSON", ct)
	}
}

func TestFileURLEndpointRequiresInitData(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/file-url?book_id=1", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestFileDownloadWithValidSignature(t *testing.T) {
	s, db := newTestServer(t)

	content := []byte("PDF-1.4 test")
	name := "book_test.pdf"
	if err := os.WriteFile(filepath.Join(s.cfg.UploadDir, name), content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	book, err := db.AddBook("Книга", "Математика", "", "", "local:"+name, "document", 0)
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}

	exp := time.Now().Add(time.Minute).Unix()
	sig := s.fileSignature(book.ID, exp)
	target := "/api/file?book_id=" + strconv.FormatInt(book.ID, 10) +
		"&exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(content) {
		t.Errorf("body = %q, want %q", rec.Body.String(), string(content))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header is missing")
	}
}

// A file whose extension is not on the inline whitelist must be forced to
// download, so an uploaded document can never run in the app origin.
func TestUnsafeExtensionIsForcedToDownload(t *testing.T) {
	s, db := newTestServer(t)

	name := "book_x.html"
	if err := os.WriteFile(filepath.Join(s.cfg.UploadDir, name), []byte("<script>1</script>"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	book, err := db.AddBook("Странный файл", "Прочее", "", "", "local:"+name, "document", 0)
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}

	exp := time.Now().Add(time.Minute).Unix()
	target := "/api/file?book_id=" + strconv.FormatInt(book.ID, 10) +
		"&exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.fileSignature(book.ID, exp)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

// The extension whitelist and content sniffing live in internal/filekind and
// are covered there; here we only check that the upload endpoint enforces auth.
func TestUploadRequiresInitData(t *testing.T) {
	s, _ := newTestServer(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "payload.html")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("<script>alert(1)</script>")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.WriteField("title", "Тест")
	_ = mw.WriteField("subject", "Тест")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without initData", rec.Code)
	}
}

func TestSanitizeUploadName(t *testing.T) {
	cases := map[string]string{
		"Angliyskiy dlya inzhenerov": "Angliyskiy_dlya_inzhenerov",
		"../../etc/passwd":           "etc_passwd",
		"   ":                        "file",
		"МУ КУБЫШКО":                 "МУ_КУБЫШКО",
	}
	for in, want := range cases {
		if got := sanitizeUploadName(in); got != want {
			t.Errorf("sanitizeUploadName(%q) = %q, want %q", in, got, want)
		}
	}
}
