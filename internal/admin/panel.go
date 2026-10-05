package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"studbot/internal/bot"
	"studbot/internal/config"
	"studbot/internal/filekind"
	"studbot/internal/storage"
)

type Server struct {
	cfg     *config.Config
	db      *storage.DB
	bots    *bot.Provider
	mux     *http.ServeMux
	srv     *http.Server
	started time.Time

	mu      sync.Mutex
	running bool
	logs    []logEntry

	loginFails  int
	blockedTill time.Time
}

const cspAdmin = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'; object-src 'none'"

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", cspAdmin)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Handler is the mux wrapped with hardening headers.
func (s *Server) Handler() http.Handler { return withSecurityHeaders(s.mux) }

type logEntry struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

func New(cfg *config.Config, db *storage.DB, bots *bot.Provider) *Server {
	return &Server{
		cfg:     cfg,
		db:      db,
		bots:    bots,
		mux:     http.NewServeMux(),
		started: time.Now(),
		running: true,
	}
}

func (s *Server) AddLog(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, logEntry{Time: time.Now(), Text: text})
	if len(s.logs) > 500 {
		s.logs = s.logs[len(s.logs)-500:]
	}
}

func (s *Server) Run(addr string) error {
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("Admin panel on http://localhost%s", addr)
	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func (s *Server) Routes() {
	s.routes()
	if s.cfg.AdminPassword != "" {
		if s.cfg.AdminOnlyLocal {
			log.Printf("Admin panel protected by password (local network only)")
		} else {
			log.Printf("Admin panel protected by password (reachable from any address)")
		}
	} else {
		log.Printf("WARNING: ADMIN_PASSWORD is empty, panel is reachable from localhost only")
	}
}

// sessionToken derives the cookie value from the password. The cookie never
// contains the password itself and becomes invalid as soon as it changes.
func (s *Server) sessionToken() string {
	mac := hmac.New(sha256.New, []byte("studbot-panel-v1:"+s.cfg.AdminPassword))
	mac.Write([]byte("session"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) setAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_auth",
		Value:    s.sessionToken(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ADMIN_ONLY_LOCAL=true restricts who may even try; it never disables
		// the password. A configured password is always required.
		if s.cfg.AdminOnlyLocal && !s.checkLocalNetwork(r) {
			s.deny(w, r, "Доступ к панели разрешён только из локальной сети", http.StatusForbidden)
			return
		}
		if s.cfg.AdminPassword == "" {
			if s.checkLoopback(r) {
				next(w, r)
				return
			}
			s.deny(w, r, "ADMIN_PASSWORD не задан: панель доступна только с localhost", http.StatusForbidden)
			return
		}
		if s.checkPassword(r) {
			next(w, r)
			return
		}
		s.deny(w, r, "Требуется пароль", http.StatusUnauthorized)
	}
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request, msg string, code int) {
	// Browsers get the login page; XHR/fetch gets JSON.
	if code == http.StatusUnauthorized && r.Header.Get("X-Requested-With") != "panel" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	writeErr(w, msg, code)
}

// requirePost rejects state-changing requests that are not POSTs, so a
// cross-site GET navigation can never mutate anything.
func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeErr(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func (s *Server) checkLoopback(r *http.Request) bool {
	ip := clientIP(r)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) checkLocalNetwork(r *http.Request) bool {
	ip := clientIP(r)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func (s *Server) checkPassword(r *http.Request) bool {
	cookie, err := r.Cookie("panel_auth")
	if err != nil || cookie.Value == "" {
		return false
	}
	return hmac.Equal([]byte(cookie.Value), []byte(s.sessionToken()))
}

const (
	maxLoginAttempts = 5
	loginBlockFor    = 30 * time.Second
)

func (s *Server) loginBlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.blockedTill)
}

func (s *Server) loginFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginFails++
	if s.loginFails >= maxLoginAttempts {
		s.loginFails = 0
		s.blockedTill = time.Now().Add(loginBlockFor)
	}
}

func (s *Server) loginSucceeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginFails = 0
	s.blockedTill = time.Time{}
}

func (s *Server) routes() {
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)
	s.mux.HandleFunc("/api/login", s.handleAPILogin)

	protected := []string{
		"/api/panel/status", "/api/panel/logs", "/api/panel/users", "/api/panel/role",
		"/api/panel/broadcast", "/api/panel/content", "/api/panel/delete",
		"/api/panel/poll/close", "/api/panel/clear", "/api/panel/add-book",
		"/api/panel/add-homework", "/api/panel/add-event", "/api/panel/upload-book",
		"/api/panel/book-file", "/api/panel/subjects",
	}
	for _, p := range protected {
		s.mux.HandleFunc(p, s.auth(handlerFor(s, p)))
	}

	s.mux.HandleFunc("/panel.js", s.handlePanelJS)
	s.mux.HandleFunc("/panel.css", s.handlePanelCSS)
	// The panel shell itself is protected too: without a valid cookie the
	// browser is redirected to /login instead of seeing the interface.
	s.mux.HandleFunc("/", s.auth(s.handleIndex))
}

func handlerFor(s *Server, path string) http.HandlerFunc {
	switch path {
	case "/api/panel/status":
		return s.handleStatus
	case "/api/panel/logs":
		return s.handleLogs
	case "/api/panel/users":
		return s.handleUsers
	case "/api/panel/role":
		return s.handleRole
	case "/api/panel/broadcast":
		return s.handleBroadcast
	case "/api/panel/content":
		return s.handleContent
	case "/api/panel/delete":
		return s.handleDelete
	case "/api/panel/poll/close":
		return s.handlePollClose
	case "/api/panel/clear":
		return s.handleClear
	case "/api/panel/add-book":
		return s.handleAddBook
	case "/api/panel/add-homework":
		return s.handleAddHomework
	case "/api/panel/add-event":
		return s.handleAddEvent
	case "/api/panel/upload-book":
		return s.handleUploadBook
	case "/api/panel/book-file":
		return s.handleBookFile
	case "/api/panel/subjects":
		return s.handleSubjects
	}
	return func(w http.ResponseWriter, r *http.Request) { writeErr(w, "not found", http.StatusNotFound) }
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminPassword == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		if s.loginBlocked() {
			s.AddLog("вход в панель заблокирован (слишком много попыток)")
			http.Redirect(w, r, "/login?err=blocked", http.StatusFound)
			return
		}
		if hmac.Equal([]byte(r.FormValue("password")), []byte(s.cfg.AdminPassword)) {
			s.loginSucceeded()
			s.setAuthCookie(w)
			s.AddLog("вход в панель выполнен")
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.loginFailed()
		s.AddLog("неудачная попытка входа в панель")
		http.Redirect(w, r, "/login?err=1", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	html := `<!DOCTYPE html><html lang="ru"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Вход — StudBot</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:"Segoe UI",system-ui,sans-serif;background:#0f151e;color:#e6edf5;
min-height:100vh;display:flex;align-items:center;justify-content:center;padding:20px}
.box{width:100%;max-width:360px;background:#1a2331;border:1px solid #2b3749;border-radius:16px;padding:26px}
h1{font-size:20px;margin-bottom:4px}
p{color:#8494a8;font-size:13px;margin-bottom:20px}
label{display:block;font-size:12px;color:#8494a8;margin-bottom:6px}
input{width:100%;padding:12px;border-radius:10px;border:1px solid #2b3749;background:#0f151e;
color:#e6edf5;font-size:15px;font-family:inherit;margin-bottom:14px}
input:focus{outline:none;border-color:#4c8dff}
button{width:100%;padding:13px;border:0;border-radius:10px;font-size:15px;font-weight:600;
background:linear-gradient(135deg,#4c8dff,#7c5cff);color:#fff;cursor:pointer;font-family:inherit}
.err{color:#ff5c62;font-size:13px;margin-bottom:14px}
</style></head><body><div class="box">
<h1>🔐 StudBot</h1><p>Панель управления ботом</p>
` + func() string {
		if r.URL.Query().Get("err") == "blocked" {
			return `<div class="err">Слишком много попыток. Подождите 30 секунд.</div>`
		}
		if r.URL.Query().Get("err") == "1" {
			return `<div class="err">Неверный пароль</div>`
		}
		return ""
	}() + `
<form method="POST" action="/login">
<label>Пароль</label>
<input type="password" name="password" autofocus autocomplete="current-password">
<button type="submit">Войти</button>
</form></div></body></html>`
	_, _ = w.Write([]byte(html))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "panel_auth", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	if s.loginBlocked() {
		writeErr(w, "Слишком много попыток. Попробуйте позже", http.StatusTooManyRequests)
		return
	}
	pass := r.FormValue("password")
	if s.cfg.AdminPassword == "" || hmac.Equal([]byte(pass), []byte(s.cfg.AdminPassword)) {
		s.loginSucceeded()
		s.setAuthCookie(w)
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	s.loginFailed()
	writeErr(w, "Неверный пароль", http.StatusUnauthorized)
}

type statusPayload struct {
	BotUser    string    `json:"bot_user"`
	BotName    string    `json:"bot_name"`
	BotOnline  bool      `json:"bot_online"`
	Uptime     string    `json:"uptime"`
	UptimeSec  int64     `json:"uptime_sec"`
	GoVersion  string    `json:"go_version"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	Goroutines int       `json:"goroutines"`
	Users      int       `json:"users"`
	Students   int       `json:"students"`
	Books      int       `json:"books"`
	Homework   int       `json:"homework"`
	Events     int       `json:"events"`
	Polls      int       `json:"polls"`
	DBSize     int64     `json:"db_size"`
	Proxy      string    `json:"proxy"`
	WebAppURL  string    `json:"webapp_url"`
	RootIDs    []int64   `json:"root_ids"`
	MemAlloc   uint64    `json:"mem_alloc"`
	PerCPU     float64   `json:"per_cpu"`
	Now        time.Time `json:"now"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	users, _ := s.db.ListUsers()
	books, _ := s.db.ListBooks("")
	hw, _ := s.db.ListHomework("")
	events, _ := s.db.ListEvents()
	polls, _ := s.db.ListPolls(false)

	students := 0
	for _, u := range users {
		if u.Role == "student" {
			students++
		}
	}

	var dbSize int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		dbSize = fi.Size()
	}

	botUser := ""
	botName := ""
	online := false
	if b := s.bots.Get(); b != nil {
		online = true
		if resp, err := b.GetMe(); err == nil {
			botUser = resp.UserName
			botName = resp.FirstName
		}
	}

	up := time.Since(s.started)
	payload := statusPayload{
		BotUser:    botUser,
		BotName:    botName,
		BotOnline:  online,
		Uptime:     up.Round(time.Second).String(),
		UptimeSec:  int64(up.Seconds()),
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Goroutines: runtime.NumGoroutine(),
		Users:      len(users),
		Students:   students,
		Books:      len(books),
		Homework:   len(hw),
		Events:     len(events),
		Polls:      len(polls),
		DBSize:     dbSize,
		Proxy:      s.cfg.ProxyURL,
		WebAppURL:  s.cfg.WebAppURL,
		RootIDs:    s.cfg.RootUserIDs,
		MemAlloc:   m.Alloc,
		PerCPU:     float64(m.Alloc) / (1024 * 1024),
		Now:        time.Now(),
	}
	writeJSON(w, payload)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]logEntry, len(s.logs))
	copy(out, s.logs)
	writeJSON(w, out)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.ListUsers()
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type row struct {
		storage.User
		Total   int     `json:"total"`
		Present int     `json:"present"`
		Percent float64 `json:"percent"`
	}
	rows := make([]row, 0, len(users))
	for _, u := range users {
		total, present, _ := s.db.GetAttendanceStats(u.ID)
		pct := 0.0
		if total > 0 {
			pct = float64(present) / float64(total) * 100
		}
		rows = append(rows, row{User: u, Total: total, Present: present, Percent: pct})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Role != rows[j].Role {
			return roleWeight(rows[i].Role) > roleWeight(rows[j].Role)
		}
		return rows[i].FullName < rows[j].FullName
	})
	writeJSON(w, rows)
}

func roleWeight(role string) int {
	switch role {
	case "root":
		return 0
	case "admin":
		return 1
	case "proforg":
		return 2
	default:
		return 3
	}
}

func (s *Server) handleRole(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	tgID, _ := strconv.ParseInt(r.FormValue("tg_id"), 10, 64)
	role := normalizeRole(r.FormValue("role"))
	if tgID == 0 || role == "" {
		writeErr(w, "bad params", http.StatusBadRequest)
		return
	}
	target, err := s.db.GetUserByTgID(tgID)
	if err != nil {
		writeErr(w, "user not found (they must message the bot first)", http.StatusNotFound)
		return
	}
	if err := s.db.SetRole(tgID, role); err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("role %d (%s) -> %s", tgID, target.FullName, role))
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		writeErr(w, "empty text", http.StatusBadRequest)
		return
	}
	role := r.FormValue("role")
	if role == "" {
		role = "all"
	}

	var users []storage.User
	var err error
	switch role {
	case "student":
		users, err = s.db.ListUsersByRole("student")
	default:
		users, err = s.db.ListUsers()
	}
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b := s.bots.Get()
	if b == nil {
		writeErr(w, "Бот не в сети — рассылка недоступна", http.StatusServiceUnavailable)
		return
	}

	sent, failed := 0, 0
	for _, u := range users {
		if err := b.SendText(u.TgID, text); err != nil {
			failed++
			continue
		}
		sent++
		time.Sleep(50 * time.Millisecond)
	}
	s.AddLog(fmt.Sprintf("broadcast (%s): sent=%d failed=%d", role, sent, failed))
	writeJSON(w, map[string]int{"sent": sent, "failed": failed})
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	books, _ := s.db.ListBooks("")
	hw, _ := s.db.ListHomework("")
	events, _ := s.db.ListEvents()
	polls, _ := s.db.ListPolls(false)
	attendance, _ := s.db.ListAttendanceByDate(time.Now().Format("2006-01-02"))
	writeJSON(w, map[string]interface{}{
		"books": books, "homework": hw, "events": events,
		"polls": polls, "attendance": attendance,
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	kind := r.FormValue("kind")
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		writeErr(w, "bad id", http.StatusBadRequest)
		return
	}

	switch kind {
	case "book":
		err = s.db.DeleteBook(id)
	case "homework":
		err = s.db.DeleteHomework(id)
	case "event":
		err = s.db.DeleteEvent(id)
	case "poll":
		err = s.db.DeletePoll(id)
	default:
		writeErr(w, "unknown kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("deleted %s #%d", kind, id))
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handlePollClose(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		writeErr(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.db.ClosePoll(id); err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("closed poll #%d", id))
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	what := r.FormValue("what")
	switch what {
	case "attendance":
		users, _ := s.db.ListUsers()
		date := time.Now().Format("2006-01-02")
		n := 0
		for _, u := range users {
			if u.Role == "student" {
				s.db.DeleteAttendance(u.ID, date)
				n++
			}
		}
		s.AddLog(fmt.Sprintf("cleared attendance for %d students (%s)", n, date))
		writeJSON(w, map[string]int{"cleared": n})
	case "logs":
		s.mu.Lock()
		s.logs = nil
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		writeErr(w, "unknown target", http.StatusBadRequest)
	}
}

func (s *Server) handleSubjects(w http.ResponseWriter, r *http.Request) {
	bookSubjects, _ := s.db.ListSubjects()
	hwSubjects, _ := s.db.ListHomeworkSubjects()
	all := map[string]bool{}
	for _, s := range bookSubjects {
		all[s] = true
	}
	for _, s := range hwSubjects {
		all[s] = true
	}
	list := make([]string, 0, len(all))
	for s := range all {
		list = append(list, s)
	}
	sort.Strings(list)
	writeJSON(w, list)
}

func (s *Server) handleAddBook(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	if title == "" || subject == "" {
		writeErr(w, "Нужны название и предмет", http.StatusBadRequest)
		return
	}
	book, err := s.db.AddBook(title, subject, strings.TrimSpace(r.FormValue("author")),
		strings.TrimSpace(r.FormValue("description")), "", "", s.rootUserID())
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("added book #%d «%s» (%s)", book.ID, book.Title, book.Subject))
	writeJSON(w, book)
}

func (s *Server) handleAddHomework(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	if title == "" || subject == "" {
		writeErr(w, "Нужны предмет и название задания", http.StatusBadRequest)
		return
	}
	hw, err := s.db.AddHomework(subject, title, strings.TrimSpace(r.FormValue("description")),
		strings.TrimSpace(r.FormValue("due_date")), s.rootUserID())
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("added homework #%d «%s» (%s)", hw.ID, hw.Title, hw.Subject))
	writeJSON(w, hw)
}

func (s *Server) handleAddEvent(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		writeErr(w, "Нужно название события", http.StatusBadRequest)
		return
	}
	event, err := s.db.AddEvent(title, strings.TrimSpace(r.FormValue("description")),
		strings.TrimSpace(r.FormValue("event_date")), s.rootUserID())
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("added event #%d «%s»", event.ID, event.Title))
	writeJSON(w, event)
}

func (s *Server) handleUploadBook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, panelMaxUploadBytes)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, "Слишком большой файл", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, "Файл не выбран", http.StatusBadRequest)
		return
	}
	defer file.Close()

	title := strings.TrimSpace(r.FormValue("title"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	if title == "" || subject == "" {
		writeErr(w, "Нужны название и предмет", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ext, err := filekind.Ext(header.Filename)
	if err != nil {
		writeErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := filekind.Sniff(file, ext); err != nil {
		writeErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	safe := fmt.Sprintf("book_%d_%s%s", time.Now().UnixNano(), sanitizeName(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))), ext)
	dest := filepath.Join(s.cfg.UploadDir, safe)

	dst, err := os.Create(dest)
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	written, err := io.Copy(dst, file)
	dst.Close()
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	book, err := s.db.AddBook(title, subject, strings.TrimSpace(r.FormValue("author")),
		strings.TrimSpace(r.FormValue("description")), "local:"+safe, "document", s.rootUserID())
	if err != nil {
		writeErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AddLog(fmt.Sprintf("uploaded book #%d «%s» (%s, %s, %d bytes)", book.ID, book.Title, book.Subject, header.Filename, written))
	writeJSON(w, book)
}

func (s *Server) handleBookFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, "bad id", http.StatusBadRequest)
		return
	}
	book, err := s.db.GetBook(id)
	if err != nil || book.AttachmentID == "" {
		writeErr(w, "У книги нет файла", http.StatusNotFound)
		return
	}
	if !strings.HasPrefix(book.AttachmentID, "local:") {
		writeErr(w, "Файл хранится в Telegram, скачай через бота", http.StatusBadRequest)
		return
	}
	name := strings.TrimPrefix(book.AttachmentID, "local:")
	if filepath.Base(name) != name {
		writeErr(w, "У книги нет файла", http.StatusNotFound)
		return
	}
	if !filekind.InlineSafe(filepath.Ext(name)) {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filepath.Join(s.cfg.UploadDir, name))
}

// panelMaxUploadBytes caps a single uploaded book file.
const panelMaxUploadBytes = 128 << 20

func (s *Server) rootUserID() int64 {
	for _, id := range s.cfg.RootUserIDs {
		if u, err := s.db.GetUserByTgID(id); err == nil {
			return u.ID
		}
	}
	return 0
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	if len(b.String()) > 60 {
		return b.String()[:60]
	}
	return b.String()
}

// Static assets are embedded so the binary does not depend on the working
// directory it was started from.
//
//go:embed static/index.html static/panel.js static/panel.css
var staticFS embed.FS

func serveStatic(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	serveStatic(w, r, "index.html", "text/html; charset=utf-8")
}

func (s *Server) handlePanelJS(w http.ResponseWriter, r *http.Request) {
	serveStatic(w, r, "panel.js", "application/javascript; charset=utf-8")
}

func (s *Server) handlePanelCSS(w http.ResponseWriter, r *http.Request) {
	serveStatic(w, r, "panel.css", "text/css; charset=utf-8")
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func normalizeRole(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "root":
		return "root"
	case "admin":
		return "admin"
	case "proforg":
		return "proforg"
	case "student":
		return "student"
	default:
		return ""
	}
}
