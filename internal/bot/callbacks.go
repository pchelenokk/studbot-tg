package bot

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"studbot/internal/storage"
)

// callbackActions maps callback data prefixes to actions. Longer, more
// specific prefixes must come before shorter ones ("admin_stats" before
// "admin"), otherwise the shorter prefix swallows the longer button.
var callbackActions = []string{
	"wizard_cancel",
	"book_open", "book_file", "book_del", "book_new",
	"hw_filter", "hw_del", "hw_new",
	"event_del", "event_new",
	"poll_view", "poll_close", "poll_new",
	"checkin",
	"rvote", "vote_start", "vote",
	"setrole", "stu",
	"admin_stats", "admin_table", "admin",
	"books", "homework", "events", "polls", "students", "menu",
}

func splitCallback(data string) (string, string) {
	for _, a := range callbackActions {
		if data == a {
			return a, ""
		}
		if strings.HasPrefix(data, a+"_") {
			return a, strings.TrimPrefix(data, a+"_")
		}
	}
	return data, ""
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	user, err := b.db.UpsertUser(cb.From.ID, cb.From.UserName, displayName(cb.From.FirstName, cb.From.LastName))
	if err != nil {
		user = &storage.User{ID: 0, TgID: cb.From.ID, FullName: displayName(cb.From.FirstName, cb.From.LastName), Role: "student"}
	}
	b.applyConfiguredRole(user)

	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	action, arg := splitCallback(cb.Data)

	switch action {
	case "menu":
		b.showMain(chatID, msgID, user)
	case "books":
		b.showBooks(chatID, msgID, user)
	case "homework":
		b.showHomework(chatID, msgID, user)
	case "events":
		b.showEvents(chatID, msgID, user)
	case "polls":
		b.showPolls(chatID, msgID, user)
	case "vote_start":
		b.startCheckin(chatID, user)
	case "students":
		b.showStudents(chatID, msgID, user)
	case "admin":
		b.showAdmin(chatID, msgID, user)
	case "book_new":
		b.needPermission(chatID, msgID, user, b.isRoot(user) || b.canManage(user), func() {
			b.dropWizard(user.TgID)
			b.startWizard(user, chatID, "book", StepBookTitle)
		})
	case "hw_new":
		b.needPermission(chatID, msgID, user, b.canManage(user), func() {
			b.dropWizard(user.TgID)
			b.startWizard(user, chatID, "hw", StepHwSubject)
		})
	case "event_new":
		b.needPermission(chatID, msgID, user, b.canManage(user), func() {
			b.dropWizard(user.TgID)
			b.startWizard(user, chatID, "event", StepEventTitle)
		})
	case "poll_new":
		b.needPermission(chatID, msgID, user, b.canManage(user), func() {
			b.dropWizard(user.TgID)
			b.startWizard(user, chatID, "poll", StepPollQuestion)
		})
	case "wizard_cancel":
		b.dropWizard(user.TgID)
		b.editOrSend(chatID, msgID, "Отменено", b.mainMenu(user))
	case "book_open":
		b.openBook(chatID, msgID, user, arg)
	case "book_file":
		b.sendBookFile(chatID, msgID, user, arg)
	case "book_del":
		b.deleteBook(chatID, msgID, user, arg)
	case "hw_del":
		b.deleteHomework(chatID, msgID, user, arg)
	case "checkin":
		b.handleCheckinVote(chatID, user, arg)
	case "hw_filter":
		subjects, err := b.db.ListHomeworkSubjects()
		if err != nil {
			b.toastFor(chatID, "❌ Не удалось получить список предметов")
			return
		}
		idx, err := strconv.Atoi(arg)
		if err != nil || idx < 0 || idx >= len(subjects) {
			b.toastFor(chatID, "❌ Предмет не найден")
			return
		}
		b.showHomeworkFiltered(chatID, msgID, user, subjects[idx])
	case "event_del":
		b.deleteEvent(chatID, msgID, user, arg)
	case "poll_view":
		b.viewPoll(chatID, msgID, user, arg)
	case "poll_close":
		b.closePoll(chatID, msgID, user, arg)
	case "vote", "rvote":
		b.vote(chatID, msgID, user, action, arg)
	case "stu":
		b.showStudent(chatID, msgID, user, arg)
	case "setrole":
		b.applyRole(chatID, msgID, user, arg)
	case "admin_stats":
		b.showStats(chatID, msgID, user)
	case "admin_table":
		b.sendTable(chatID, msgID, user, arg)
	case "noop":
	default:
		b.answer(cb, "Неизвестная кнопка")
	}
}

func (b *Bot) answer(cb *tgbotapi.CallbackQuery, text string) {
	if err := b.tg.AnswerCallbackQuery(cb.ID, text, false); err != nil {
		log.Printf("answer cb: %v", err)
	}
}

func (b *Bot) toast(cb *tgbotapi.CallbackQuery, text string) {
	if err := b.tg.AnswerCallbackQuery(cb.ID, text, false); err != nil {
		log.Printf("answer cb: %v", err)
	}
}

func (b *Bot) needPermission(chatID int64, msgID int, user *storage.User, allowed bool, fn func()) {
	if !allowed {
		b.toastFor(chatID, "❌ Недостаточно прав")
		return
	}
	fn()
}

func (b *Bot) toastFor(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("toast: %v", err)
	}
}

func (b *Bot) showMain(chatID int64, msgID int, user *storage.User) {
	b.editOrSend(chatID, msgID, fmt.Sprintf("👋 <b>%s</b>\n\nРоль: %s %s\n\nВыбери раздел:",
		escHTML(user.FullName), b.roleEmoji(user.Role), b.roleName(user.Role)), b.mainMenu(user))
}

func (b *Bot) showBooks(chatID int64, msgID int, user *storage.User) {
	books, err := b.db.ListBooks("")
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	b.editOrSend(chatID, msgID, b.booksText(books, user), b.bookListKeyboard(books, user))
}

func (b *Bot) openBook(chatID int64, msgID int, user *storage.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	book, err := b.db.GetBook(id)
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Книга не найдена", b.mainMenu(user))
		return
	}

	var info []string
	info = append(info, fmt.Sprintf("<b>%s</b>", escHTML(book.Title)))
	info = append(info, fmt.Sprintf("Предмет: %s", escHTML(book.Subject)))
	if book.Author != "" {
		info = append(info, fmt.Sprintf("Автор: %s", escHTML(book.Author)))
	}
	if book.Description != "" {
		info = append(info, fmt.Sprintf("Описание: %s", escHTML(book.Description)))
	}
	if book.AttachmentID != "" {
		info = append(info, "Файл прикреплён ✅")
	} else {
		info = append(info, "Файла нет")
	}
	b.editOrSend(chatID, msgID, strings.Join(info, "\n"), b.bookCardKeyboard(book, user))
}

func (b *Bot) sendBookFile(chatID int64, msgID int, user *storage.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	book, err := b.db.GetBook(id)
	if err != nil || book.AttachmentID == "" {
		b.toastFor(chatID, "❌ У книги нет файла")
		return
	}
	if strings.HasPrefix(book.AttachmentID, "local:") {
		b.toastFor(chatID, "❌ Файл хранится в приложении, открой книгу в мини-приложении")
		return
	}

	caption := fmt.Sprintf("📚 %s\n%s", escHTML(book.Title), escHTML(book.Subject))
	if book.AttachmentType == "photo" {
		b.sendPhoto(chatID, book.AttachmentID, caption)
	} else {
		b.sendDoc(chatID, book.AttachmentID, caption)
	}
}

func (b *Bot) deleteBook(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Удалять книги может только староста")
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	if err := b.db.DeleteBook(id); err != nil {
		b.toastFor(chatID, "❌ "+err.Error())
		return
	}
	books, _ := b.db.ListBooks("")
	b.editOrSend(chatID, msgID, b.booksText(books, user), b.bookListKeyboard(books, user))
}

func (b *Bot) handleCheckinVote(chatID int64, user *storage.User, arg string) {
	parts := strings.SplitN(arg, "_", 2)
	if len(parts) < 2 {
		return
	}
	checkinID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	status := parts[1]
	switch status {
	case "present", "absent", "late":
	default:
		return
	}

	checkin, err := b.db.GetCheckin(checkinID)
	if err != nil {
		b.toastFor(chatID, "❌ Перекличка не найдена")
		return
	}
	if checkin.Status != "active" {
		b.toastFor(chatID, "⏳ Перекличка уже закрыта")
		return
	}

	if err := b.db.VoteCheckin(checkinID, user.ID, status); err != nil {
		b.toastFor(chatID, "❌ "+err.Error())
		return
	}
	b.toastFor(chatID, "✅ "+statusLabel(status)+" — отметка засчитана")
}

func (b *Bot) showHomeworkFiltered(chatID int64, msgID int, user *storage.User, subject string) {
	items, err := b.db.ListHomework(subject)
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	text := b.homeworkText(items) + "\nФильтр: <b>" + escHTML(subject) + "</b>"
	b.editOrSend(chatID, msgID, text, b.homeworkFilteredKeyboard(items, user))
}

func (b *Bot) showHomework(chatID int64, msgID int, user *storage.User) {
	items, err := b.db.ListHomework("")
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	if len(items) == 0 {
		b.editOrSend(chatID, msgID, "📝 <b>Домашних заданий пока нет</b>", b.homeworkMenu(user))
		return
	}
	text := b.homeworkText(items) + "Выбери предмет, чтобы увидеть только его задания."
	b.editOrSend(chatID, msgID, text, b.homeworkSubjectFilter(items, user))
}

func (b *Bot) deleteHomework(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Недостаточно прав")
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	b.db.DeleteHomework(id)
	items, _ := b.db.ListHomework("")
	b.editOrSend(chatID, msgID, b.homeworkText(items), b.homeworkMenu(user))
}

func (b *Bot) showEvents(chatID int64, msgID int, user *storage.User) {
	events, err := b.db.ListEvents()
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	b.editOrSend(chatID, msgID, b.eventsText(events), b.eventsMenu(user))
}

func (b *Bot) deleteEvent(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Недостаточно прав")
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	b.db.DeleteEvent(id)
	events, _ := b.db.ListEvents()
	b.editOrSend(chatID, msgID, b.eventsText(events), b.eventsMenu(user))
}

func (b *Bot) showPolls(chatID int64, msgID int, user *storage.User) {
	polls, err := b.db.ListPolls(true)
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	if len(polls) == 0 {
		text := "📊 <b>Активных опросов нет</b>"
		if b.canManage(user) {
			text += "\n\nНажми «Создать опрос» — бот спросит вопрос и варианты."
		}
		b.editOrSend(chatID, msgID, text, b.pollsMenu(user))
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Активные опросы</b> — %d шт.\n\n", len(polls)))
	for _, p := range polls {
		sb.WriteString(fmt.Sprintf("• #%d %s\n", p.ID, p.Question))
		for i, o := range p.Options {
			sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, o))
		}
		sb.WriteString("\n")
	}
	b.editOrSend(chatID, msgID, sb.String(), b.pollListKeyboard(polls, user))
}

func (b *Bot) viewPoll(chatID int64, msgID int, user *storage.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	poll, err := b.db.GetPoll(id)
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Опрос не найден", b.mainMenu(user))
		return
	}
	b.editOrSend(chatID, msgID, b.pollCardText(poll), b.pollVoteKeyboard(poll, user))
}

func (b *Bot) pollCardText(poll *storage.Poll) string {
	results, _ := b.db.GetPollResults(poll.ID)
	counts := map[int]int{}
	total := 0
	for _, r := range results {
		counts[r.OptionIndex] = r.Count
		total += r.Count
	}

	text := fmt.Sprintf("📊 <b>Опрос #%d</b>\n\n%s\n\n", poll.ID, escHTML(poll.Question))
	if total == 0 {
		text += "Голосов пока нет. Голосуй кнопками ниже."
	} else {
		text += fmt.Sprintf("<b>Результаты (%d голосов):</b>\n", total)
		for i, opt := range poll.Options {
			c := counts[i]
			pct := 0
			if total > 0 {
				pct = c * 100 / total
			}
			text += fmt.Sprintf("%d. %s — %d (%d%%) %s\n", i+1, escHTML(opt), c, pct, repeatBar(float64(pct)))
		}
	}
	return text
}

func (b *Bot) pollVoteKeyboard(poll *storage.Poll, user *storage.User) tgbotapi.InlineKeyboardMarkup {
	prefix := "vote_"
	if b.isRoot(user) {
		prefix = "rvote_"
	}
	var vote []tgbotapi.InlineKeyboardButton
	for i, opt := range poll.Options {
		label := opt
		if len(label) > 25 {
			label = truncate(opt, 25) + "…"
		}
		vote = append(vote, tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("%s%d_%d", prefix, poll.ID, i)))
	}
	rows := [][]tgbotapi.InlineKeyboardButton{vote}
	if b.canManage(user) && poll.IsActive {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("🔒 Закрыть опрос", fmt.Sprintf("poll_close_%d", poll.ID)),
		})
	}
	rows = append(rows, backRow("polls", "К опросам"))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) closePoll(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.canManage(user) {
		b.toastFor(chatID, "❌ Недостаточно прав")
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	b.db.ClosePoll(id)
	b.toastFor(chatID, "🔒 Опрос закрыт")
	b.showPolls(chatID, msgID, user)
}

func (b *Bot) vote(chatID int64, msgID int, user *storage.User, action, arg string) {
	parts := strings.SplitN(arg, "_", 2)
	if len(parts) < 2 {
		return
	}
	pollID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	optIdx, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	poll, err := b.db.GetPoll(pollID)
	if err != nil {
		b.toastFor(chatID, "❌ Опрос не найден")
		return
	}
	if !poll.IsActive {
		b.toastFor(chatID, "Опрос уже закрыт")
		return
	}
	if optIdx < 0 || optIdx >= len(poll.Options) {
		return
	}
	if err := b.db.Vote(pollID, user.ID, optIdx); err != nil {
		b.toastFor(chatID, "❌ "+err.Error())
		return
	}
	b.editOrSend(chatID, msgID, b.pollCardText(poll), b.pollVoteKeyboard(poll, user))
}

func (b *Bot) showStudents(chatID int64, msgID int, user *storage.User) {
	if !b.canManage(user) {
		b.toastFor(chatID, "❌ Недостаточно прав")
		b.editOrSend(chatID, msgID, "❌ Недостаточно прав", b.mainMenu(user))
		return
	}

	users, err := b.db.ListUsers()
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 <b>Студенты и роли</b> — %d чел.\n\n", len(users)))
	rootCount, profCount, studentCount := 0, 0, 0
	for _, u := range users {
		switch u.Role {
		case "root", "admin":
			rootCount++
		case "proforg":
			profCount++
		default:
			studentCount++
		}
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", b.roleEmoji(u.Role), escHTML(u.FullName), b.roleName(u.Role)))
	}
	sb.WriteString(fmt.Sprintf("\n👑 Старост: %d | 📋 Профоргов: %d | 👤 Студентов: %d\n", rootCount, profCount, studentCount))

	if b.isRoot(user) {
		sb.WriteString("\n<b>Как назначить роль:</b> нажми на студента ниже → выбери роль кнопкой.")
	} else {
		sb.WriteString("\nНазначать роли может староста.")
	}
	b.editOrSend(chatID, msgID, sb.String(), b.studentsKeyboard(user))
}

func (b *Bot) showStudent(chatID int64, msgID int, user *storage.User, arg string) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return
	}
	target, err := b.db.GetUserByID(id)
	if err != nil {
		b.toastFor(chatID, "❌ Пользователь не найден")
		return
	}

	total, present, _ := b.db.GetAttendanceStats(target.ID)
	var stats string
	if total > 0 {
		stats = fmt.Sprintf("\n📈 Посещаемость: %d из %d (%.0f%%)", present, total, float64(present)/float64(total)*100)
	} else {
		stats = "\n📈 Отметок посещаемости пока нет"
	}

	text := fmt.Sprintf("%s <b>%s</b>\n\nTelegram ID: <code>%d</code>\nРоль: %s %s%s",
		b.roleEmoji(target.Role), escHTML(target.FullName), target.TgID, b.roleEmoji(target.Role), b.roleName(target.Role), stats)

	if b.canManage(user) {
		text += "\n\n<b>Что сделать?</b>"
	} else {
		text += "\n\nСправка по студенту."
	}
	b.editOrSend(chatID, msgID, text, b.studentCardKeyboard(target, user))
}

func (b *Bot) applyRole(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Назначать роли может только староста")
		return
	}
	parts := strings.SplitN(arg, "_", 2)
	if len(parts) < 2 {
		return
	}
	targetID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	role := normalizeRole(parts[1])
	if role == "" {
		return
	}

	target, err := b.db.GetUserByID(targetID)
	if err != nil {
		b.toastFor(chatID, "❌ Пользователь не найден")
		return
	}
	if target.Role == role {
		b.toastFor(chatID, fmt.Sprintf("%s уже %s", target.FullName, b.roleName(role)))
		return
	}
	if err := b.db.SetRole(target.TgID, role); err != nil {
		b.toastFor(chatID, "❌ "+err.Error())
		return
	}
	b.toastFor(chatID, fmt.Sprintf("✅ %s → %s %s", target.FullName, b.roleEmoji(role), b.roleName(role)))

	users, _ := b.db.ListUsers()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 <b>Студенты и роли</b> — %d чел.\n\n", len(users)))
	for _, u := range users {
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", b.roleEmoji(u.Role), escHTML(u.FullName), b.roleName(u.Role)))
	}
	sb.WriteString("\nНажми на студента, чтобы изменить роль.")
	b.editOrSend(chatID, msgID, sb.String(), b.studentsKeyboard(user))
}

func (b *Bot) showAdmin(chatID int64, msgID int, user *storage.User) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Раздел доступен только старосте")
		b.editOrSend(chatID, msgID, "❌ Недостаточно прав", b.mainMenu(user))
		return
	}
	text := "⚙️ <b>Управление</b>\n\n" +
		"Здесь староста может:\n" +
		"• посмотреть и скачать таблицу посещаемости\n" +
		"• удалять книги, ДЗ и события\n" +
		"• назначать роли студентам и профоргам\n" +
		"• смотреть статистику посещаемости\n\n" +
		"Выбери действие:"
	b.editOrSend(chatID, msgID, text, b.adminMenu())
}

func (b *Bot) showStats(chatID int64, msgID int, user *storage.User) {
	if !b.isRoot(user) {
		b.toastFor(chatID, "❌ Недостаточно прав")
		return
	}
	users, _ := b.db.ListUsersByRole("student")
	var sb strings.Builder
	sb.WriteString("📈 <b>Статистика посещаемости</b>\n\n")
	if len(users) == 0 {
		sb.WriteString("Студентов пока нет")
	}
	for _, u := range users {
		total, present, _ := b.db.GetAttendanceStats(u.ID)
		if total == 0 {
			sb.WriteString(fmt.Sprintf("• %s — нет отметок\n", escHTML(u.FullName)))
			continue
		}
		pct := float64(present) / float64(total) * 100
		sb.WriteString(fmt.Sprintf("• %s — %d из %d (%.0f%%) %s\n", escHTML(u.FullName), present, total, pct, repeatBar(pct)))
	}
	b.editOrSend(chatID, msgID, sb.String(), b.adminMenu())
}
