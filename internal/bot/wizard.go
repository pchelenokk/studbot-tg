package bot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"studbot/internal/storage"
	"studbot/internal/xlsx"
)

type Step int

const (
	StepNone Step = iota
	StepBookTitle
	StepBookSubject
	StepBookAuthor
	StepBookFile
	StepHwSubject
	StepHwTitle
	StepHwDesc
	StepHwDue
	StepEventTitle
	StepEventDesc
	StepEventDate
	StepPollQuestion
	StepPollOptions
	StepCheckinDate
	StepCheckinDuration
	StepCheckinSubject
	StepTextDone
)

type Wizard struct {
	Flow  string
	Await Step
	Data  map[string]string
	// File is per-user: it lives inside the wizard so two users uploading at
	// the same time can never attach each other's file to a book.
	File pendingFile
}

type pendingFile struct {
	fileID     string
	attachType string
	fileName   string
}

func (w *Wizard) set(key, value string) { w.Data[key] = value }
func (w *Wizard) get(key string) string { return w.Data[key] }

func cancelKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "wizard_cancel"),
		},
	)
}

func (b *Bot) startWizard(user *storage.User, chatID int64, flow string, first Step) {
	w := &Wizard{Flow: flow, Await: first, Data: map[string]string{}}
	b.putWizard(user.TgID, w)
	b.prompt(user, chatID, w)
}

func (b *Bot) prompt(user *storage.User, chatID int64, w *Wizard) {
	text := b.promptText(w)
	if text == "" {
		b.delWizard(user.TgID)
		b.send(chatID, "Действие отменено", b.mainMenu(user))
		return
	}
	b.send(chatID, text, cancelKeyboard())
}

func (b *Bot) promptText(w *Wizard) string {
	switch w.Await {
	case StepBookTitle:
		return "📚 <b>Новая книга — шаг 1 из 4</b>\n\nНапиши название книги:"
	case StepBookSubject:
		return "📚 <b>Шаг 2 из 4</b>\n\nПредмет (например, Математика):"
	case StepBookAuthor:
		return "📚 <b>Шаг 3 из 4</b>\n\nАвтор книги.\nЕсли неизвестен — напиши <code>-</code>"
	case StepBookFile:
		return "📚 <b>Шаг 4 из 4</b>\n\nПришли файл книги (PDF, DOCX и т.п.).\nЕсли файла нет — напиши <code>-</code>"
	case StepHwSubject:
		return "📝 <b>Новое ДЗ — шаг 1 из 4</b>\n\nПредмет:"
	case StepHwTitle:
		return "📝 <b>Шаг 2 из 4</b>\n\nЧто нужно сделать:"
	case StepHwDesc:
		return "📝 <b>Шаг 3 из 4</b>\n\nПодробности задания.\nЕсли не нужны — <code>-</code>"
	case StepHwDue:
		return "📝 <b>Шаг 4 из 4</b>\n\nДедлайн в формате <code>ДД.ММ.ГГГГ</code>.\nЕсли нет — <code>-</code>"
	case StepEventTitle:
		return "📅 <b>Новое событие — шаг 1 из 3</b>\n\nНазвание события:"
	case StepEventDesc:
		return "📅 <b>Шаг 2 из 3</b>\n\nОписание.\nЕсли не нужно — <code>-</code>"
	case StepEventDate:
		return "📅 <b>Шаг 3 из 3</b>\n\nДата в формате <code>ДД.ММ.ГГГГ</code>.\nЕсли нет — <code>-</code>"
	case StepPollQuestion:
		return "📊 <b>Новый опрос — шаг 1 из 2</b>\n\nТекст вопроса:"
	case StepPollOptions:
		return "📊 <b>Шаг 2 из 2</b>\n\nВарианты ответа, каждый с новой строки.\nНужно от 2 до 10 вариантов."
	case StepCheckinDate:
		return "🗳 <b>Перекличка — шаг 1 из 3</b>\n\nНапиши дату в формате <code>ДД.ММ.ГГГГ</code>.\nДля сегодняшней просто напиши <code>-</code>"
	case StepCheckinDuration:
		return "⏳ <b>Шаг 2 из 3</b>\n\nСколько минут будет открыта перекличка?\nНапример: <code>15</code>"
	case StepCheckinSubject:
		return "📘 <b>Шаг 3 из 3</b>\n\nПредмет переклички.\nЕсли не важно — <code>-</code>"
	default:
		return ""
	}
}

func (b *Bot) dropWizard(tgID int64) {
	b.delWizard(tgID)
}

func (b *Bot) isDash(text string) bool {
	return text == "-" || text == "—"
}

func (b *Bot) handleWizardText(msg *tgbotapi.Message, user *storage.User) bool {
	w := b.getWizard(msg.From.ID)
	if w == nil {
		return false
	}

	raw := strings.TrimSpace(msg.Text)
	skip := b.isDash(raw)
	value := raw

	switch w.Await {
	case StepBookTitle:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Название не может быть пустым. Напиши его ещё раз.", cancelKeyboard())
			return true
		}
		w.set("title", value)
		w.Await = StepBookSubject
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepBookSubject:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Предмет обязателен. Напиши его ещё раз.", cancelKeyboard())
			return true
		}
		w.set("subject", value)
		w.Await = StepBookAuthor
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepBookAuthor:
		w.set("author", value)
		w.Await = StepBookFile
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepBookFile:
		if raw == "" {
			b.send(msg.Chat.ID, "❌ Пришли файл книги (PDF, DOCX и т.п.) или напиши <code>-</code>, если файла нет.", cancelKeyboard())
			return true
		}
		return b.finishBook(msg, user, w)

	case StepHwSubject:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Предмет обязателен. Напиши его ещё раз.", cancelKeyboard())
			return true
		}
		w.set("subject", value)
		w.Await = StepHwTitle
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepHwTitle:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Название задания обязательно. Напиши ещё раз.", cancelKeyboard())
			return true
		}
		w.set("title", value)
		w.Await = StepHwDesc
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepHwDesc:
		w.set("description", value)
		w.Await = StepHwDue
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepHwDue:
		w.set("due", normalizeDate(value))
		b.finishHomework(msg.Chat.ID, user, w)
		return true

	case StepEventTitle:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Название события обязательно. Напиши ещё раз.", cancelKeyboard())
			return true
		}
		w.set("title", value)
		w.Await = StepEventDesc
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepEventDesc:
		w.set("description", value)
		w.Await = StepEventDate
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepEventDate:
		w.set("date", normalizeDate(value))
		b.finishEvent(msg.Chat.ID, user, w)
		return true

	case StepPollQuestion:
		if skip || value == "" {
			b.send(msg.Chat.ID, "❌ Вопрос обязателен. Напиши его ещё раз.", cancelKeyboard())
			return true
		}
		w.set("question", value)
		w.Await = StepPollOptions
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepPollOptions:
		b.finishPoll(msg.Chat.ID, user, w, value)
		return true

	case StepCheckinDate:
		date := normalizeDate(value)
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		w.set("date", date)
		w.Await = StepCheckinDuration
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepCheckinDuration:
		minutes, err := strconv.Atoi(value)
		if err != nil || minutes < 1 || minutes > 240 {
			b.send(msg.Chat.ID, "❌ Нужно число минут от 1 до 240. Например: <code>15</code>", cancelKeyboard())
			return true
		}
		w.set("minutes", value)
		w.Await = StepCheckinSubject
		b.prompt(user, msg.Chat.ID, w)
		return true

	case StepCheckinSubject:
		w.set("subject", value)
		b.finishCheckin(msg.Chat.ID, user, w)
		return true
	}

	b.dropWizard(msg.From.ID)
	return true
}

func (b *Bot) finishBook(msg *tgbotapi.Message, user *storage.User, w *Wizard) bool {
	file := w.File
	w.File = pendingFile{}
	book, err := b.db.AddBook(
		w.get("title"), w.get("subject"), w.get("author"),
		"", file.fileID, file.attachType, user.ID,
	)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка сохранения: "+escHTML(err.Error()), b.mainMenu(user))
		b.dropWizard(user.TgID)
		return true
	}
	b.dropWizard(user.TgID)

	if book.AttachmentID == "" {
		b.send(msg.Chat.ID, fmt.Sprintf("✅ Книга <b>%s</b> (%s) добавлена в библиотеку", escHTML(book.Title), escHTML(book.Subject)), b.mainMenu(user))
	} else {
		b.send(msg.Chat.ID, fmt.Sprintf("✅ Книга <b>%s</b> (%s) с файлом добавлена в библиотеку", escHTML(book.Title), escHTML(book.Subject)), b.mainMenu(user))
	}
	return true
}

func (b *Bot) finishHomework(chatID int64, user *storage.User, w *Wizard) {
	hw, err := b.db.AddHomework(w.get("subject"), w.get("title"), w.get("description"), w.get("due"), user.ID)
	b.dropWizard(user.TgID)
	if err != nil {
		b.send(chatID, "❌ Ошибка сохранения: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	due := ""
	if hw.DueDate != "" {
		due = ", дедлайн " + escHTML(hw.DueDate)
	}
	b.send(chatID, fmt.Sprintf("✅ ДЗ добавлено: <b>%s</b> (%s%s)", escHTML(hw.Title), escHTML(hw.Subject), due), b.homeworkMenu(user))
}

func (b *Bot) finishEvent(chatID int64, user *storage.User, w *Wizard) {
	event, err := b.db.AddEvent(w.get("title"), w.get("description"), w.get("date"), user.ID)
	b.dropWizard(user.TgID)
	if err != nil {
		b.send(chatID, "❌ Ошибка сохранения: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	date := ""
	if event.EventDate != "" {
		date = ", дата " + escHTML(event.EventDate)
	}
	b.send(chatID, fmt.Sprintf("✅ Событие добавлено: <b>%s</b>%s", escHTML(event.Title), date), b.eventsMenu(user))
}

func (b *Bot) finishPoll(chatID int64, user *storage.User, w *Wizard, raw string) {
	var options []string
	for _, line := range strings.Split(raw, "\n") {
		opt := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if opt != "" {
			options = append(options, opt)
		}
	}
	if len(options) < 2 {
		b.send(chatID, "❌ Нужно минимум 2 варианта, каждый с новой строки. Попробуй ещё раз.", cancelKeyboard())
		return
	}
	if len(options) > 10 {
		options = options[:10]
	}

	poll, err := b.db.CreatePoll(w.get("question"), options, user.ID)
	b.dropWizard(user.TgID)
	if err != nil {
		b.send(chatID, "❌ Ошибка сохранения: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	b.showPoll(chatID, poll, user)
}

func (b *Bot) handleBookFile(msg *tgbotapi.Message, user *storage.User) bool {
	w := b.getWizard(msg.From.ID)
	if w == nil || w.Flow != "book" || w.Await != StepBookFile {
		return false
	}

	var file pendingFile
	if msg.Document != nil {
		file = pendingFile{fileID: msg.Document.FileID, attachType: "document", fileName: msg.Document.FileName}
	} else if len(msg.Photo) > 0 {
		file = pendingFile{fileID: msg.Photo[len(msg.Photo)-1].FileID, attachType: "photo", fileName: "photo.jpg"}
	} else {
		return false
	}

	w.File = file
	w.Await = StepTextDone
	b.finishBook(msg, user, w)
	return true
}

// RecoverInterruptedCheckins closes check-ins left open by a restart: their
// timers died with the process, so they would stay open forever. Returns how
// many were closed.
func (b *Bot) RecoverInterruptedCheckins() int {
	expired, err := b.db.ListExpiredActiveCheckins(time.Now().Unix())
	if err != nil {
		log.Printf("list expired checkins: %v", err)
		return 0
	}

	closed := 0
	for _, c := range expired {
		if _, err := b.db.CloseCheckin(c.ID); err != nil {
			log.Printf("close checkin %d: %v", c.ID, err)
			continue
		}
		closed++
		b.finalizeCheckin(c.ID, c.CreatedBy)
	}
	if closed > 0 {
		log.Printf("closed %d check-ins interrupted by restart", closed)
	}
	return closed
}

// startCheckin begins the interactive check-in creation flow.
func (b *Bot) startCheckin(chatID int64, user *storage.User) {
	if !b.canManage(user) {
		b.toastFor(chatID, "❌ Перекличку может создавать только староста или профорг")
		return
	}
	b.dropWizard(user.TgID)
	b.startWizard(user, chatID, "checkin", StepCheckinDate)
}

func (b *Bot) finishCheckin(chatID int64, user *storage.User, w *Wizard) {
	minutes, _ := strconv.Atoi(w.get("minutes"))
	if minutes <= 0 {
		minutes = 15
	}
	subject := w.get("subject")
	if subject == "" {
		subject = "Перекличка"
	}

	checkin, err := b.db.CreateCheckin(subject, w.get("date"), int64(minutes*60), user.ID)
	b.dropWizard(user.TgID)
	if err != nil {
		b.send(chatID, "❌ Ошибка создания переклички: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}

	text := fmt.Sprintf(
		"🗳 <b>Перекличка создана</b>\n\n📘 %s\n📅 %s\n⏳ %d минут\n\nОтметьтесь кнопками ниже. Можно менять ответ, пока перекличка открыта.\n\nЗакрыть досрочно: <code>/closevote %d</code>",
		escHTML(subject), escHTML(checkin.Date), minutes, checkin.ID)

	kb := b.checkinKeyboard(checkin.ID)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = kb
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send checkin: %v", err)
	}

	b.scheduleCheckinClose(checkin)
}

func (b *Bot) scheduleCheckinClose(checkin *storage.Checkin) {
	time.AfterFunc(time.Duration(checkin.DurationSeconds)*time.Second, func() {
		b.finalizeCheckin(checkin.ID, checkin.CreatedBy)
	})
}

// finalizeCheckin closes a check-in and pushes the attendance spreadsheet.
// It is safe to call twice: the storage layer performs the transition atomically.
func (b *Bot) finalizeCheckin(checkinID, byUserID int64) {
	changed, err := b.db.CloseCheckin(checkinID)
	if err != nil {
		log.Printf("close checkin %d: %v", checkinID, err)
		return
	}
	if !changed {
		return
	}

	checkin, err := b.db.GetCheckin(checkinID)
	if err != nil {
		log.Printf("get checkin %d: %v", checkinID, err)
		return
	}

	votes, err := b.db.GetCheckinVotes(checkinID)
	if err != nil {
		log.Printf("get votes %d: %v", checkinID, err)
		return
	}

	creator, err := b.db.GetUserByID(byUserID)
	if err != nil || creator == nil {
		log.Printf("checkin %d: creator not found", checkinID)
		return
	}

	path, err := b.exportAttendance(checkin)
	if err != nil {
		log.Printf("export checkin %d: %v", checkinID, err)
		return
	}

	msg := tgbotapi.NewDocument(creator.TgID, tgbotapi.FilePath(path))
	msg.Caption = fmt.Sprintf(
		"✅ <b>Перекличка #%d завершена</b>\n\n📘 %s\n📅 %s\n👥 Отметились: %d\n\nТаблица прикреплена.",
		checkin.ID, escHTML(checkin.Subject), escHTML(checkin.Date), len(votes))
	msg.ParseMode = "HTML"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send attendance file: %v", err)
	}

	absent, present := countStatuses(votes)
	if len(votes) > 0 {
		note := tgbotapi.NewMessage(creator.TgID, fmt.Sprintf(
			"📊 Итоги: ✅ %d пришли, ❌ %d отсутствуют.", present, absent))
		if _, err := b.api.Send(note); err != nil {
			log.Printf("send checkin summary: %v", err)
		}
	}
}

func countStatuses(rows []storage.CheckinVoteRow) (absent, present int) {
	for _, r := range rows {
		switch r.Status {
		case "present":
			present++
		default:
			absent++
		}
	}
	return absent, present
}

// attendanceFilename is stable on purpose: the workbook is a single cumulative
// document that grows with every closed check-in, not a new file each time.
const attendanceFilename = "attendance.xlsx"

// exportAttendance rebuilds the whole attendance workbook: one sheet per ISO
// week, rows grouped by class session (subject + date), sorted chronologically.
func (b *Bot) exportAttendance(checkin *storage.Checkin) (string, error) {
	allRows, err := b.db.AllClosedCheckinVotes()
	if err != nil {
		return "", err
	}

	type weekKey [2]int
	byWeek := map[weekKey][]storage.CheckinVoteRow{}
	seen := map[[2]int]bool{}

	for _, r := range allRows {
		d, err := time.Parse("2006-01-02", r.Date)
		if err != nil {
			continue
		}
		year, week := d.ISOWeek()
		k := [2]int{year, week}
		byWeek[k] = append(byWeek[k], r)
		seen[k] = true
	}

	// Always keep the sheet of the week we just closed, even if nobody voted.
	d, err := time.Parse("2006-01-02", checkin.Date)
	if err == nil {
		year, week := d.ISOWeek()
		seen[weekKey{year, week}] = true
	}

	keys := xlsx.SortWeekKeys(seen)
	if len(keys) == 0 {
		return "", fmt.Errorf("no weeks to export")
	}

	header := []string{"Дата", "Предмет", "ФИО", "Telegram", "Статус", "Отметился в"}

	sheets := make([]xlsx.Sheet, 0, len(keys))
	for _, k := range keys {
		rows := byWeek[k]
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Date != rows[j].Date {
				return rows[i].Date < rows[j].Date
			}
			if rows[i].Subject != rows[j].Subject {
				return rows[i].Subject < rows[j].Subject
			}
			return rows[i].FullName < rows[j].FullName
		})

		sheet := xlsx.Sheet{
			Name:   xlsx.WeekSheetName(k[0], k[1]),
			Header: header,
			Widths: []float64{12, 22, 32, 22, 16, 20},
		}
		for _, r := range rows {
			telegram := "—"
			if r.Username != "" {
				telegram = "@" + r.Username
			}
			sheet.Rows = append(sheet.Rows, []string{
				r.Date, r.Subject, displayUserName(r.FullName, r.Username),
				telegram, statusLabel(r.Status), r.VotedAt,
			})
		}
		sheets = append(sheets, sheet)
	}

	if err := os.MkdirAll(b.cfg.ExportDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(b.cfg.ExportDir, attendanceFilename)
	if err := xlsx.Write(path, sheets); err != nil {
		return "", err
	}

	log.Printf("attendance workbook updated: %s (%d sheets)", path, len(sheets))
	return path, nil
}

// weekSummary reports how many rows each week holds, used in the caption.
func weekSummary(checkin *storage.Checkin) string {
	d, err := time.Parse("2006-01-02", checkin.Date)
	if err != nil {
		return ""
	}
	year, week := d.ISOWeek()
	return fmt.Sprintf("%s, %s", xlsx.WeekSheetName(year, week), checkin.Date)
}

func statusLabel(status string) string {
	switch status {
	case "present":
		return "пришёл"
	case "absent":
		return "отсутствует"
	case "late":
		return "опоздал"
	default:
		return status
	}
}

func displayUserName(fullName, username string) string {
	if n := strings.TrimSpace(fullName); n != "" {
		return n
	}
	if username != "" {
		return "@" + username
	}
	return "Без имени"
}

// displayUser renders a user for humans: name when known, @nick as a fallback.
func displayUser(u *storage.User) string {
	if u == nil {
		return "Неизвестный"
	}
	if n := strings.TrimSpace(u.FullName); n != "" {
		return n
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return fmt.Sprintf("ID %d", u.TgID)
}

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	layouts := []string{"02.01.2006", "2006-01-02", "02/01/2006", "2.1.2006", "01.02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			if layout == "01.02" {
				return t.Format("2006-01-02")
			}
			return t.Format("2006-01-02")
		}
	}
	log.Printf("unparsed date: %q", s)
	return s
}
