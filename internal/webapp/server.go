package webapp

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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"studbot/internal/bot"
	"studbot/internal/config"
	"studbot/internal/filekind"
	"studbot/internal/storage"
)

type Server struct {
	cfg  *config.Config
	db   *storage.DB
	bots *bot.Provider
	mux  *http.ServeMux
	srv  *http.Server
}

// cspWebApp allows the Telegram SDK and nothing else. The app is embedded in
// Telegram Web, so only Telegram origins may frame it.
const cspWebApp = "default-src 'self'; " +
	"script-src 'self' https://telegram.org; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'self' https://*.telegram.org https://*.t.me; " +
	"base-uri 'none'; form-action 'none'; object-src 'none'"

func withSecurityHeaders(next http.Handler, csp string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func New(cfg *config.Config, db *storage.DB, bots *bot.Provider) *Server {
	s := &Server{
		cfg:  cfg,
		db:   db,
		bots: bots,
		mux:  http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler is the mux wrapped with security headers.
func (s *Server) Handler() http.Handler { return withSecurityHeaders(s.mux, cspWebApp) }

func (s *Server) Run() error {
	s.srv = &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		// Files may be streamed from Telegram, so the write timeout is generous.
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
	log.Printf("WebApp server listening on %s", s.cfg.ListenAddr)
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

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/app.js", s.handleAppJS)
	s.mux.HandleFunc("/style.css", s.handleStyleCSS)
	s.mux.HandleFunc("/api/me", s.authMiddleware(s.handleMe))
	s.mux.HandleFunc("/api/state", s.authMiddleware(s.handleState))
	s.mux.HandleFunc("/api/action", s.authMiddleware(s.handleAction))
	// The browser cannot attach the initData header to window.open(), so this
	// authenticated endpoint hands out a short-lived signed download URL.
	s.mux.HandleFunc("/api/file-url", s.authMiddleware(s.handleFileURL))
	// /api/file itself is reachable without a header, but only with a valid
	// signature produced by /api/file-url.
	s.mux.HandleFunc("/api/file", s.handleFile)
	s.mux.HandleFunc("/api/upload", s.authMiddleware(s.handleUpload))
}

// Static assets are embedded so the binary does not depend on the working
// directory it was started from.
//
//go:embed static/index.html static/app.js static/style.css
var staticFS embed.FS

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	setStaticHeaders(w)
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveStatic(w, r, "index.html", "text/html; charset=utf-8")
}

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "app.js", "application/javascript; charset=utf-8")
}

func (s *Server) handleStyleCSS(w http.ResponseWriter, r *http.Request) {
	s.serveStatic(w, r, "style.css", "text/css; charset=utf-8")
}

func setStaticHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// writeError answers with a JSON error body so the client can show the real
// message instead of a bare HTTP status.
func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// fileTokenTTL is how long a signed download link stays valid.
const fileTokenTTL = 10 * time.Minute

func (s *Server) fileSignature(bookID, exp int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.Token))
	fmt.Fprintf(mac, "studbot-file:%d:%d", bookID, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) signedFileURL(bookID int64) string {
	exp := time.Now().Add(fileTokenTTL).Unix()
	return fmt.Sprintf("/api/file?book_id=%d&exp=%d&sig=%s", bookID, exp, s.fileSignature(bookID, exp))
}

func (s *Server) validFileSignature(bookID, exp int64, sig string) bool {
	if sig == "" || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.fileSignature(bookID, exp)))
}

func (s *Server) handleFileURL(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.URL.Query().Get("book_id"), 10, 64)
	if err != nil || bookID <= 0 {
		writeError(w, "Некорректный book_id", http.StatusBadRequest)
		return
	}
	attID, _, err := s.db.GetBookFile(bookID)
	if err != nil || attID == "" {
		writeError(w, "У книги нет файла", http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"url": s.signedFileURL(bookID)})
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.Header.Get("X-Telegram-Init-Data")
		if initData == "" {
			initData = r.URL.Query().Get("initData")
		}
		if initData == "" {
			writeError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := s.validateInitData(initData)
		if err != nil {
			writeError(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := contextWithUser(r.Context(), user)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) validateInitData(initData string) (*storage.User, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return nil, err
	}

	dataCheckString := buildDataCheckString(vals)

	secretKey := hmacSHA256([]byte("WebAppData"), []byte(s.cfg.Token))
	hash := hmacSHA256(secretKey, []byte(dataCheckString))

	expectedHash := hex.EncodeToString(hash)
	if expectedHash != vals.Get("hash") {
		return nil, fmt.Errorf("invalid hash")
	}

	authDateStr := vals.Get("auth_date")
	if authDateStr == "" {
		return nil, fmt.Errorf("no auth_date")
	}
	authDate, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("bad auth_date")
	}
	age := time.Since(time.Unix(authDate, 0))
	if age > 24*time.Hour {
		return nil, fmt.Errorf("init data expired")
	}
	if age < -10*time.Minute {
		return nil, fmt.Errorf("auth_date is in the future")
	}

	userJSON := vals.Get("user")
	if userJSON == "" {
		return nil, fmt.Errorf("no user data")
	}

	var tgUser struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	}
	if err := json.Unmarshal([]byte(userJSON), &tgUser); err != nil {
		return nil, err
	}

	fullName := tgUser.FirstName
	if tgUser.LastName != "" {
		fullName += " " + tgUser.LastName
	}

	user, err := s.db.UpsertUser(tgUser.ID, tgUser.Username, fullName)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func buildDataCheckString(vals url.Values) string {
	var pairs []string
	for key, values := range vals {
		if key == "hash" {
			continue
		}
		for _, v := range values {
			pairs = append(pairs, key+"="+v)
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\n")
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	jsonResponse(w, user)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	books, _ := s.db.ListBooks("")
	hw, _ := s.db.ListHomework("")
	polls, _ := s.db.ListPolls(true)
	events, _ := s.db.ListEvents()
	users, _ := s.db.ListUsers()
	today := time.Now().Format("2006-01-02")
	attendance, _ := s.db.ListAttendanceByDate(today)
	checkins, _ := s.db.ListCheckins(20)

	type attendanceEntry struct {
		UserID int64  `json:"user_id"`
		Status string `json:"status"`
	}
	markMap := make([]attendanceEntry, 0, len(attendance))
	for _, a := range attendance {
		markMap = append(markMap, attendanceEntry{UserID: a.UserID, Status: a.Status})
	}

	// Students may not see the group roster with Telegram IDs, nor other
	// people's attendance marks.
	if !s.canManage(user) {
		for i := range users {
			users[i].TgID = 0
			users[i].Username = ""
		}
		own := make([]attendanceEntry, 0, 1)
		for _, m := range markMap {
			if m.UserID == user.ID {
				own = append(own, m)
			}
		}
		markMap = own
	}

	state := map[string]interface{}{
		"user":       user,
		"books":      books,
		"homework":   hw,
		"polls":      polls,
		"events":     events,
		"users":      users,
		"attendance": markMap,
		"checkins":   checkins,
		"date":       today,
	}

	jsonResponse(w, state)
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Action string                 `json:"action"`
		Params map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Bad request", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "add_book":
		s.actionAddBook(w, user, req.Params)
	case "delete_book":
		s.actionDeleteBook(w, user, req.Params)
	case "add_homework":
		s.actionAddHomework(w, user, req.Params)
	case "delete_homework":
		s.actionDeleteHomework(w, user, req.Params)
	case "mark_attendance":
		s.actionMarkAttendance(w, user, req.Params)
	case "checkin_create":
		s.actionCheckinCreate(w, user, req.Params)
	case "checkin_vote":
		s.actionCheckinVote(w, user, req.Params)
	case "checkin_close":
		s.actionCheckinClose(w, user, req.Params)
	case "create_poll":
		s.actionCreatePoll(w, user, req.Params)
	case "close_poll":
		s.actionClosePoll(w, user, req.Params)
	case "delete_poll":
		s.actionDeletePoll(w, user, req.Params)
	case "vote_poll":
		s.actionVotePoll(w, user, req.Params)
	case "add_event":
		s.actionAddEvent(w, user, req.Params)
	case "delete_event":
		s.actionDeleteEvent(w, user, req.Params)
	case "set_role":
		s.actionSetRole(w, user, req.Params)
	case "search":
		s.actionSearch(w, user, req.Params)
	case "file_url":
		s.actionFileURL(w, user, req.Params)
	case "mark_all_present":
		s.actionMarkAll(w, user)
	case "reset_attendance":
		s.actionResetAttendance(w, user)
	default:
		writeError(w, "Неизвестное действие", http.StatusBadRequest)
	}
}

func (s *Server) actionFileURL(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	bookID := getInt64(params, "book_id")
	if bookID <= 0 {
		writeError(w, "Некорректный book_id", http.StatusBadRequest)
		return
	}
	attID, _, err := s.db.GetBookFile(bookID)
	if err != nil || attID == "" {
		writeError(w, "У книги нет файла", http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"url": s.signedFileURL(bookID)})
}

func (s *Server) actionCheckinCreate(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	date := getString(params, "date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	minutes := int(getInt64(params, "minutes"))
	if minutes < 1 || minutes > 240 {
		minutes = 15
	}
	subject := getString(params, "subject")
	if subject == "" {
		subject = "Перекличка"
	}

	checkin, err := s.db.CreateCheckin(subject, date, int64(minutes*60), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, checkin)
}

func (s *Server) actionCheckinVote(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	id := getInt64(params, "checkin_id")
	status := getString(params, "status")
	if id == 0 {
		http.Error(w, "checkin_id required", http.StatusBadRequest)
		return
	}
	switch status {
	case "present", "absent", "late":
	default:
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	checkin, err := s.db.GetCheckin(id)
	if err != nil {
		http.Error(w, "check-in not found", http.StatusNotFound)
		return
	}
	if checkin.Status != "active" {
		http.Error(w, "check-in is closed", http.StatusBadRequest)
		return
	}
	if err := s.db.VoteCheckin(id, user.ID, status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionCheckinClose(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	id := getInt64(params, "checkin_id")
	if id == 0 {
		http.Error(w, "checkin_id required", http.StatusBadRequest)
		return
	}
	if _, err := s.db.CloseCheckin(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionMarkAll(w http.ResponseWriter, user *storage.User) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}
	students, err := s.db.ListUsersByRole("student")
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	today := time.Now().Format("2006-01-02")
	for _, st := range students {
		s.db.MarkAttendance(st.ID, today, "present", user.ID)
	}
	jsonResponse(w, map[string]int{"marked": len(students)})
}

func (s *Server) actionResetAttendance(w http.ResponseWriter, user *storage.User) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}
	students, _ := s.db.ListUsersByRole("student")
	today := time.Now().Format("2006-01-02")
	for _, st := range students {
		s.db.DeleteAttendance(st.ID, today)
	}
	jsonResponse(w, map[string]int{"cleared": len(students)})
}

func (s *Server) actionAddBook(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	title := clip(getString(params, "title"), 200)
	subject := clip(getString(params, "subject"), 120)
	author := clip(getString(params, "author"), 120)
	description := clip(getString(params, "description"), 2000)

	if title == "" || subject == "" {
		writeError(w, "Нужны название и предмет", http.StatusBadRequest)
		return
	}

	book, err := s.db.AddBook(title, subject, author, description, "", "", user.ID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, book)
}

func (s *Server) actionDeleteBook(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := getInt64(params, "id")
	if id == 0 {
		writeError(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteBook(id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionAddHomework(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	subject := clip(getString(params, "subject"), 120)
	title := clip(getString(params, "title"), 200)
	description := clip(getString(params, "description"), 2000)
	dueDate := clip(getString(params, "due_date"), 40)

	if subject == "" || title == "" {
		writeError(w, "Нужны предмет и задание", http.StatusBadRequest)
		return
	}

	hw, err := s.db.AddHomework(subject, title, description, dueDate, user.ID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, hw)
}

func (s *Server) actionDeleteHomework(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := getInt64(params, "id")
	if id == 0 {
		writeError(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteHomework(id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionMarkAttendance(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	studentID := getInt64(params, "student_id")
	status := getString(params, "status")
	if status == "" {
		status = "present"
	}
	if status != "present" && status != "absent" && status != "late" {
		writeError(w, "Недопустимый статус отметки", http.StatusBadRequest)
		return
	}
	if _, err := s.db.GetUserByID(studentID); err != nil {
		writeError(w, "Пользователь не найден", http.StatusNotFound)
		return
	}

	today := time.Now().Format("2006-01-02")
	if err := s.db.MarkAttendance(studentID, today, status, user.ID); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionCreatePoll(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	question := clip(getString(params, "question"), 300)
	optionsRaw, ok := params["options"].([]interface{})
	if !ok || len(optionsRaw) < 2 {
		writeError(w, "Нужно минимум 2 варианта ответа", http.StatusBadRequest)
		return
	}

	var options []string
	for _, o := range optionsRaw {
		if s, ok := o.(string); ok {
			if s = clip(s, 120); s != "" {
				options = append(options, s)
			}
		}
		if len(options) == 10 {
			break
		}
	}

	if question == "" || len(options) < 2 {
		writeError(w, "Нужны вопрос и минимум 2 варианта ответа", http.StatusBadRequest)
		return
	}

	poll, err := s.db.CreatePoll(question, options, user.ID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, poll)
}

func (s *Server) actionClosePoll(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := getInt64(params, "id")
	if id == 0 {
		writeError(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.db.ClosePoll(id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionDeletePoll(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := getInt64(params, "id")
	if id == 0 {
		writeError(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeletePoll(id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionVotePoll(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	pollID := getInt64(params, "poll_id")
	optionIndex := int(getInt64(params, "option_index"))

	if pollID == 0 {
		writeError(w, "Poll ID required", http.StatusBadRequest)
		return
	}

	poll, err := s.db.GetPoll(pollID)
	if err != nil {
		writeError(w, "Poll not found", http.StatusNotFound)
		return
	}

	if !poll.IsActive {
		writeError(w, "Poll is closed", http.StatusBadRequest)
		return
	}

	if optionIndex < 0 || optionIndex >= len(poll.Options) {
		writeError(w, "Invalid option", http.StatusBadRequest)
		return
	}

	if err := s.db.Vote(pollID, user.ID, optionIndex); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionAddEvent(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	title := clip(getString(params, "title"), 200)
	description := clip(getString(params, "description"), 2000)
	eventDate := clip(getString(params, "event_date"), 40)

	if title == "" {
		writeError(w, "Нужно название события", http.StatusBadRequest)
		return
	}

	event, err := s.db.AddEvent(title, description, eventDate, user.ID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, event)
}

func (s *Server) actionDeleteEvent(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := getInt64(params, "id")
	if id == 0 {
		writeError(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteEvent(id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionSetRole(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	if !s.isRoot(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	tgID := getInt64(params, "tg_id")
	role := getString(params, "role")

	if tgID == 0 || (role != "root" && role != "proforg" && role != "student") {
		writeError(w, "Invalid params", http.StatusBadRequest)
		return
	}

	if err := s.db.SetRole(tgID, role); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) actionSearch(w http.ResponseWriter, user *storage.User, params map[string]interface{}) {
	query := clip(getString(params, "query"), 200)
	if query == "" {
		writeError(w, "Нужен поисковый запрос", http.StatusBadRequest)
		return
	}

	books, _ := s.db.SearchBooks(query)
	hw, _ := s.db.SearchHomework(query)
	events, _ := s.db.SearchEvents(query)

	jsonResponse(w, map[string]interface{}{
		"books":    books,
		"homework": hw,
		"events":   events,
	})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	bookIDStr := r.URL.Query().Get("book_id")
	if bookIDStr == "" {
		writeError(w, "book_id required", http.StatusBadRequest)
		return
	}

	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil || bookID <= 0 {
		writeError(w, "Invalid book_id", http.StatusBadRequest)
		return
	}

	exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if !s.validFileSignature(bookID, exp, r.URL.Query().Get("sig")) {
		writeError(w, "Ссылка недействительна или устарела", http.StatusUnauthorized)
		return
	}

	attID, attType, err := s.db.GetBookFile(bookID)
	if err != nil {
		writeError(w, "File not found", http.StatusNotFound)
		return
	}

	if attID == "" {
		writeError(w, "No attachment", http.StatusNotFound)
		return
	}

	var path string
	if strings.HasPrefix(attID, "local:") {
		filename := strings.TrimPrefix(attID, "local:")
		if filepath.Base(filename) != filename {
			writeError(w, "File not found", http.StatusNotFound)
			return
		}
		path = filepath.Join(s.cfg.UploadDir, filename)
		if _, err := os.Stat(path); err != nil {
			writeError(w, "File not found", http.StatusNotFound)
			return
		}
	} else {
		b := s.bots.Get()
		if b == nil {
			writeError(w, "Бот не в сети — файл из Telegram сейчас недоступен", http.StatusServiceUnavailable)
			return
		}
		filePath, err := b.GetFilePath(attID)
		if err != nil {
			writeError(w, "Telegram error", http.StatusInternalServerError)
			return
		}
		ext := ".bin"
		if attType == "photo" {
			ext = ".jpg"
		} else if attType == "document" {
			ext = ".pdf"
		}
		destName := fmt.Sprintf("book_%d%s", bookID, ext)
		path, err = b.DownloadFile(filePath, s.cfg.UploadDir, destName)
		if err != nil {
			writeError(w, "Download error", http.StatusInternalServerError)
			return
		}
	}

	serveDownload(w, r, path)
}

func serveDownload(w http.ResponseWriter, r *http.Request, path string) {
	ext := filepath.Ext(path)
	if !filekind.InlineSafe(ext) {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !s.canManage(user) {
		writeError(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, "Файл слишком большой или повреждён", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, "File required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	title := clip(r.FormValue("title"), 200)
	subject := clip(r.FormValue("subject"), 120)
	author := clip(r.FormValue("author"), 120)
	description := clip(r.FormValue("description"), 2000)

	if title == "" || subject == "" {
		writeError(w, "Нужны название и предмет", http.StatusBadRequest)
		return
	}

	ext, err := filekind.Ext(header.Filename)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := filekind.Sniff(file, ext); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	safeName := fmt.Sprintf("user_%d_%d_%s%s", user.ID, time.Now().UnixNano(), sanitizeUploadName(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))), ext)
	destPath := filepath.Join(s.cfg.UploadDir, safeName)

	dst, err := os.Create(destPath)
	if err != nil {
		writeError(w, "Server error", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		_ = os.Remove(destPath)
		writeError(w, "Server error", http.StatusInternalServerError)
		return
	}

	attType := "document"
	if strings.HasPrefix(header.Header.Get("Content-Type"), "image/") && filekind.InlineSafe(ext) && ext != ".pdf" {
		attType = "photo"
	}

	book, err := s.db.AddBook(title, subject, author, description, "local:"+safeName, attType, user.ID)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, book)
}

// maxUploadBytes caps a single uploaded file.
const maxUploadBytes = 128 << 20

// clip trims whitespace and limits the length of user supplied text.
func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

func sanitizeUploadName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я', r == 'ё', r == 'Ё':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "file"
	}
	if len([]rune(out)) > 40 {
		out = string([]rune(out)[:40])
	}
	return out
}

func (s *Server) canManage(user *storage.User) bool {
	return user.Role == "root" || user.Role == "admin" || user.Role == "proforg"
}

func (s *Server) isRoot(user *storage.User) bool {
	return user.Role == "root" || user.Role == "admin"
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return int64(val)
		case int64:
			return val
		case int:
			return int64(val)
		}
	}
	return 0
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
