package bot

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"studbot/internal/storage"
)

func (b *Bot) mainMenu(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		{
			tgbotapi.NewInlineKeyboardButtonData("📚 Библиотека", "books"),
			tgbotapi.NewInlineKeyboardButtonData("📝 Домашка", "homework"),
		},
		{tgbotapi.NewInlineKeyboardButtonData("📊 Опросы", "polls")},
		{tgbotapi.NewInlineKeyboardButtonData("📅 События", "events")},
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("🗳 Перекличка", "vote_start"),
		})
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("👥 Студенты", "students"),
		})
	}
	if b.isRoot(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Управление", "admin"),
		})
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func backRow(target, label string) []tgbotapi.InlineKeyboardButton {
	return []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("⬅️ "+label, target),
	}
}

func (b *Bot) booksMenu(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить книгу", "book_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) homeworkMenu(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	if b.isRoot(user) {
		items, _ := b.db.ListHomework("")
		for _, h := range items {
			rows = append(rows, []tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить: "+truncate(h.Title, 20), fmt.Sprintf("hw_del_%d", h.ID)),
			})
		}
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить ДЗ", "hw_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) eventsMenu(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	if b.isRoot(user) {
		events, _ := b.db.ListEvents()
		for _, e := range events {
			rows = append(rows, []tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить: "+truncate(e.Title, 20), fmt.Sprintf("event_del_%d", e.ID)),
			})
		}
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить событие", "event_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) pollsMenu(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Создать опрос", "poll_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) checkinKeyboard(checkinID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅ Пришёл", fmt.Sprintf("checkin_%d_present", checkinID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отсутствую", fmt.Sprintf("checkin_%d_absent", checkinID)),
		},
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⏳ Опоздал", fmt.Sprintf("checkin_%d_late", checkinID)),
		},
	)
}

func (b *Bot) adminMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("📊 Таблица посещаемости", "admin_table"),
			tgbotapi.NewInlineKeyboardButtonData("📈 Статистика", "admin_stats"),
		},
		backRow("students", "👥 Студенты"),
		backRow("menu", "Меню"),
	)
}

func (b *Bot) booksText(books []storage.Book, user *storage.User) string {
	if len(books) == 0 {
		if b.canManage(user) {
			return "📚 <b>Библиотека пуста</b>\n\nНажми «Добавить книгу», чтобы загрузить первую книгу."
		}
		return "📚 <b>Библиотека пока пуста</b>"
	}

	bySubject := map[string][]storage.Book{}
	var order []string
	for _, bk := range books {
		if _, ok := bySubject[bk.Subject]; !ok {
			order = append(order, bk.Subject)
		}
		bySubject[bk.Subject] = append(bySubject[bk.Subject], bk)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📚 <b>Библиотека</b> — %d книг\n\n", len(books)))
	for _, subject := range order {
		sb.WriteString(fmt.Sprintf("<b>%s</b>\n", escHTML(subject)))
		for _, bk := range bySubject[subject] {
			icon := "📄"
			if bk.AttachmentID != "" {
				icon = "📎"
			}
			author := ""
			if bk.Author != "" {
				author = " — " + escHTML(bk.Author)
			}
			sb.WriteString(fmt.Sprintf("   %s %s%s\n", icon, escHTML(bk.Title), author))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Нажми на книгу, чтобы открыть или скачать файл.")
	return sb.String()
}

func (b *Bot) bookListKeyboard(books []storage.Book, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, bk := range books {
		label := "📄 " + fit(bk.Title, 28)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("book_open_%d", bk.ID)),
		})
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить книгу", "book_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) bookCardKeyboard(book *storage.Book, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	if book.AttachmentID != "" {
		label := "📂 Скачать файл"
		if book.AttachmentType == "photo" {
			label = "🖼 Открыть изображение"
		}
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("book_file_%d", book.ID)),
		})
	}
	if b.isRoot(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить книгу", fmt.Sprintf("book_del_%d", book.ID)),
		})
	}
	rows = append(rows, backRow("books", "К библиотеке"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) homeworkText(items []storage.Homework) string {
	if len(items) == 0 {
		return "📝 <b>Домашних заданий пока нет</b>"
	}

	bySubject := map[string][]storage.Homework{}
	var order []string
	for _, h := range items {
		if _, ok := bySubject[h.Subject]; !ok {
			order = append(order, h.Subject)
		}
		bySubject[h.Subject] = append(bySubject[h.Subject], h)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📝 <b>Домашние задания</b> — %d шт. по %d предметам\n\n", len(items), len(order)))
	for _, subject := range order {
		sb.WriteString(fmt.Sprintf("📘 <b>%s</b>\n", escHTML(subject)))
		for _, h := range bySubject[subject] {
			sb.WriteString(fmt.Sprintf("   • %s", escHTML(h.Title)))
			if h.DueDate != "" {
				sb.WriteString(fmt.Sprintf(" <i>⏰ %s</i>", escHTML(h.DueDate)))
			}
			sb.WriteString("\n")
			if h.Description != "" {
				sb.WriteString(fmt.Sprintf("     <i>%s</i>\n", escHTML(h.Description)))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// homeworkSubjectFilter lists homework subjects. Callback data carries the
// subject index, not the subject itself, so long or unusual subject names can
// never exceed Telegram's 64-byte callback_data limit.
func (b *Bot) homeworkSubjectFilter(items []storage.Homework, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	subjects, err := b.db.ListHomeworkSubjects()
	if err != nil {
		log.Printf("list homework subjects: %v", err)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, s := range subjects {
		label := "📘 " + truncate(s, 22)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("hw_filter_%d", i)),
		})
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить ДЗ", "hw_new"),
		})
	}
	rows = append(rows, backRow("homework", "Все предметы"), backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) homeworkFilteredKeyboard(items []storage.Homework, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	if b.isRoot(user) {
		for _, h := range items {
			rows = append(rows, []tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить: "+truncate(h.Title, 20), fmt.Sprintf("hw_del_%d", h.ID)),
			})
		}
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить ДЗ", "hw_new"),
		})
	}
	rows = append(rows, backRow("homework", "Все предметы"), backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) eventsText(events []storage.Event) string {
	if len(events) == 0 {
		return "📅 <b>Событий пока нет</b>"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 <b>События</b> — %d шт.\n\n", len(events)))
	for _, e := range events {
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", escHTML(e.Title)))
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   <i>%s</i>\n", escHTML(e.Description)))
		}
		if e.EventDate != "" {
			sb.WriteString(fmt.Sprintf("   🗓 %s\n", escHTML(e.EventDate)))
		}
	}
	return sb.String()
}

func (b *Bot) showPoll(chatID int64, poll *storage.Poll, user *storage.User) {
	text := fmt.Sprintf("📊 <b>Опрос #%d</b>\n\n%s\n\nГолосуй кнопками ниже:", poll.ID, escHTML(poll.Question))
	for i, opt := range poll.Options {
		text += fmt.Sprintf("\n%d. %s", i+1, escHTML(opt))
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = b.pollVoteKeyboard(poll, user)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("show poll: %v", err)
	}
}

func (b *Bot) pollListKeyboard(polls []storage.Poll, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range polls {
		label := "📊 " + fit(p.Question, 28)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("poll_view_%d", p.ID)),
		})
	}
	if b.canManage(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Создать опрос", "poll_new"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) markKeyboard(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅✅ Отметить всех присутствующими", "mark_all_present"),
			tgbotapi.NewInlineKeyboardButtonData("🆕 Новый день (очистить)", "mark_reset"),
		},
		backRow("menu", "Меню"),
	)
}

func (b *Bot) studentsKeyboard(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	users, err := b.db.ListUsers()
	if err != nil {
		return tgbotapi.NewInlineKeyboardMarkup(backRow("menu", "Меню"))
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users {
		if u.Role == "root" || u.Role == "admin" {
			continue
		}
		label := fmt.Sprintf("%s %s", b.roleEmoji(u.Role), u.FullName)
		if utf8.RuneCountInString(label) > 28 {
			label = fmt.Sprintf("%s %s…", b.roleEmoji(u.Role), truncate(u.FullName, 22))
		}
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("stu_%d", u.ID)),
		})
	}
	if b.isRoot(user) {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Управление", "admin"),
		})
	}
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) studentCardKeyboard(target *storage.User, viewer *storage.User) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton

	if b.canManage(viewer) && target.Role != "root" && target.Role != "admin" {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅ «"+b.roleName("student")+"»", fmt.Sprintf("setrole_%d_student", target.ID)),
			tgbotapi.NewInlineKeyboardButtonData("📋 «"+b.roleName("proforg")+"»", fmt.Sprintf("setrole_%d_proforg", target.ID)),
		})
	}
	if b.isRoot(viewer) && target.Role != "root" && target.Role != "admin" {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("👑 «"+b.roleName("root")+"»", fmt.Sprintf("setrole_%d_root", target.ID)),
		})
	}
	if b.isRoot(viewer) && (target.Role == "root" || target.Role == "admin") && target.TgID != viewer.TgID {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⬇️ Снять права старосты", fmt.Sprintf("setrole_%d_student", target.ID)),
		})
	}

	rows = append(rows, backRow("students", "К студентам"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) attendanceKeyboard(user *storage.User) tgbotapi.InlineKeyboardMarkup {
	students, err := b.db.ListUsersByRole("student")
	if err != nil || len(students) == 0 {
		return tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Меню", "menu"),
			},
		)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, s := range students {
		name := truncate(s.FullName, 18)
		present := []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅ "+name, fmt.Sprintf("att_%d_present", s.ID)),
		}
		absent := []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("❌ "+name, fmt.Sprintf("att_%d_absent", s.ID)),
		}
		rows = append(rows, append(present, absent...))
	}

	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("✅✅ Отметить всех", "mark_all_present"),
	})
	rows = append(rows, backRow("menu", "Меню"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) attendanceText(records []storage.Attendance, users []storage.User, date string) string {
	marked := map[int64]string{}
	byID := map[int64]storage.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	for _, r := range records {
		marked[r.UserID] = r.Status
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>Посещаемость на %s</b>\n\n", date))

	presentCount := 0
	for _, u := range users {
		if u.Role != "student" {
			continue
		}
		status := "⬜ не отмечен"
		switch marked[u.ID] {
		case "present":
			status = "🟢 пришёл"
			presentCount++
		case "absent":
			status = "🔴 отсутствует"
		case "late":
			status = "🟡 опоздал"
		}
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", b.roleEmoji(u.Role), escHTML(u.FullName), status))
	}
	sb.WriteString(fmt.Sprintf("\nОтмечено: 🟢 %d\n", presentCount))
	sb.WriteString("\nНажми ✅ или ❌ рядом со студентом, чтобы изменить отметку.")
	return sb.String()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func today() string { return time.Now().Format("2006-01-02") }
