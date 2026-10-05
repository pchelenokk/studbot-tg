package bot

import (
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"studbot/internal/storage"
)

func (b *Bot) handleCommand(msg *tgbotapi.Message, user *storage.User) {
	switch msg.Command() {
	case "start", "menu":
		b.cmdStart(msg, user)
	case "help":
		b.cmdHelp(msg, user)
	case "books":
		b.cmdBooks(msg, user)
	case "homework":
		b.cmdHomework(msg, user)
	case "events":
		b.cmdEvents(msg, user)
	case "polls", "poll":
		b.cmdPolls(msg, user)
	case "vote":
		b.cmdVote(msg, user)
	case "closevote":
		b.cmdCloseVote(msg, user)
	case "stats":
		b.cmdStats(msg, user)
	case "table", "attendance":
		b.cmdTable(msg, user)
	case "cancel":
		b.dropWizard(user.TgID)
		b.send(msg.Chat.ID, "Отменено", b.mainMenu(user))
	case "setrole":
		b.cmdSetRole(msg, user)
	default:
		b.send(msg.Chat.ID, "Неизвестная команда. Нажми кнопку ниже или /help", b.mainMenu(user))
	}
}

func (b *Bot) cmdStart(msg *tgbotapi.Message, user *storage.User) {
	text := fmt.Sprintf("👋 Привет, <b>%s</b>!\n\nЯ бот для старосты группы. Твоя роль: %s %s\n\nВыбери раздел:",
		escHTML(user.FullName), b.roleEmoji(user.Role), b.roleName(user.Role))
	b.send(msg.Chat.ID, text, b.mainMenu(user))
}

func (b *Bot) cmdHelp(msg *tgbotapi.Message, user *storage.User) {
	text := "<b>Как пользоваться ботом</b>\n\nВсё делается кнопками в меню — просто нажимай и отвечай на вопросы бота.\n\n" +
		"<b>Разделы:</b>\n" +
		"📚 <b>Библиотека</b> — книги по предметам, файлы открываются кнопкой\n" +
		"📝 <b>Домашка</b> — список ДЗ с дедлайнами\n" +
		"🗳 <b>Перекличка</b> — отметка посещаемости голосованием\n" +
		"📊 <b>Опросы</b> — создание опросов и голосование кнопками\n" +
		"📅 <b>События</b> — важные даты группы\n\n" +
		"<b>Команды:</b>\n" +
		"/books — библиотека\n" +
		"/homework — домашние задания\n" +
		"/vote — начать перекличку (только староста/профорг)\n" +
		"/closevote <id> — завершить перекличку досрочно\n" +
		"/polls — опросы\n" +
		"/events — события\n" +
		"/stats — статистика посещаемости\n" +
		"/table — скачать таблицу посещаемости (Excel, листы по неделям)\n" +
		"/table 40 — только за 40-ю неделю\n" +
		"/setrole <ник> <роль> — назначить роль по юзернейму (только староста)\n" +
		"/cancel — отменить текущее действие\n\n" +
		"<b>Права ролей:</b>\n" +
		"👑 Староста — всё: контент, переклички, назначение ролей\n" +
		"📋 Профорг — контент, переклички\n" +
		"👤 Студент — смотрит контент, голосует в перекличках и опросах"

	if b.cfg.WebAppURL != "" {
		text += "\n\n📱 <b>Приложение</b> — кнопка «Приложение» в меню бота. Там то же самое, но удобнее на телефоне."
	}
	b.send(msg.Chat.ID, text, b.mainMenu(user))
}

func (b *Bot) cmdBooks(msg *tgbotapi.Message, user *storage.User) {
	books, err := b.db.ListBooks("")
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	b.send(msg.Chat.ID, b.booksText(books, user), b.bookListKeyboard(books, user))
}

func (b *Bot) cmdHomework(msg *tgbotapi.Message, user *storage.User) {
	items, err := b.db.ListHomework("")
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	if len(items) == 0 {
		b.send(msg.Chat.ID, "📝 <b>Домашних заданий пока нет</b>", b.homeworkMenu(user))
		return
	}
	text := b.homeworkText(items) + "Выбери предмет, чтобы увидеть только его задания."
	b.send(msg.Chat.ID, text, b.homeworkSubjectFilter(items, user))
}

func (b *Bot) cmdEvents(msg *tgbotapi.Message, user *storage.User) {
	events, err := b.db.ListEvents()
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	b.send(msg.Chat.ID, b.eventsText(events), b.eventsMenu(user))
}

func (b *Bot) cmdPolls(msg *tgbotapi.Message, user *storage.User) {
	polls, err := b.db.ListPolls(true)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	if len(polls) == 0 {
		text := "📊 <b>Активных опросов нет</b>"
		if b.canManage(user) {
			text += "\n\nНажми «Создать опрос» — бот спросит вопрос и варианты."
		}
		b.send(msg.Chat.ID, text, b.pollsMenu(user))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Активные опросы</b> — %d шт.\n\n", len(polls)))
	for _, p := range polls {
		sb.WriteString(fmt.Sprintf("• #%d %s\n", p.ID, escHTML(p.Question)))
		for i, o := range p.Options {
			sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, escHTML(o)))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Нажми на опрос, чтобы проголосовать.")
	b.send(msg.Chat.ID, sb.String(), b.pollListKeyboard(polls, user))
}

func (b *Bot) cmdVote(msg *tgbotapi.Message, user *storage.User) {
	if !b.canManage(user) {
		b.send(msg.Chat.ID, "❌ Перекличку может создавать только староста или профорг", b.mainMenu(user))
		return
	}
	b.dropWizard(user.TgID)
	b.startWizard(user, msg.Chat.ID, "checkin", StepCheckinDate)
}

func (b *Bot) cmdCloseVote(msg *tgbotapi.Message, user *storage.User) {
	if !b.canManage(user) {
		b.send(msg.Chat.ID, "❌ Завершать перекличку может только староста или профорг", b.mainMenu(user))
		return
	}
	idStr := strings.TrimSpace(msg.CommandArguments())
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		b.send(msg.Chat.ID, "Формат: <code>/closevote <id></code>. Номер переклички виден в сообщении о её создании.", b.mainMenu(user))
		return
	}
	b.finalizeCheckin(id, user.ID)
	b.send(msg.Chat.ID, "⏳ Завершаю перекличку и готовлю таблицу…", b.mainMenu(user))
}

func (b *Bot) cmdStats(msg *tgbotapi.Message, user *storage.User) {
	users, err := b.db.ListUsersByRole("student")
	if err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	if len(users) == 0 {
		b.send(msg.Chat.ID, "Студентов пока нет", b.mainMenu(user))
		return
	}

	var sb strings.Builder
	sb.WriteString("📈 <b>Статистика посещаемости</b>\n\n")
	for _, u := range users {
		total, present, _ := b.db.GetAttendanceStats(u.ID)
		if total == 0 {
			sb.WriteString(fmt.Sprintf("• %s — нет отметок\n", escHTML(displayUser(&u))))
			continue
		}
		pct := float64(present) / float64(total) * 100
		bar := repeatBar(pct)
		sb.WriteString(fmt.Sprintf("• %s — %d из %d (%.0f%%) %s\n", escHTML(displayUser(&u)), present, total, pct, bar))
	}
	b.send(msg.Chat.ID, sb.String(), b.mainMenu(user))
}

func repeatBar(pct float64) string {
	filled := int(pct / 10)
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + "]"
}

func (b *Bot) cmdSetRole(msg *tgbotapi.Message, user *storage.User) {
	if !b.isRoot(user) {
		b.send(msg.Chat.ID, "❌ Назначать роли может только староста", b.mainMenu(user))
		return
	}
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 2 {
		b.send(msg.Chat.ID, "Формат: <code>/setrole @username proforg</code>\n\nРоли: <code>root</code>, <code>proforg</code>, <code>student</code>\n\nИли нажми «Студенты» → выбери человека → назначь роль кнопкой.",
			b.studentsKeyboard(user))
		return
	}

	role := normalizeRole(args[len(args)-1])
	if role == "" {
		b.send(msg.Chat.ID, "❌ Роль должна быть: root, proforg или student", b.studentsKeyboard(user))
		return
	}
	username := strings.TrimPrefix(strings.TrimSpace(args[0]), "@")

	target, err := b.db.GetUserByUsername(username)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Пользователь <b>"+escHTML(username)+"</b> не найден. Он должен сначала написать боту (/start), а его ник должен быть заполнен в Telegram.",
			b.studentsKeyboard(user))
		return
	}
	if err := b.db.SetRole(target.TgID, role); err != nil {
		b.send(msg.Chat.ID, "❌ Ошибка: "+escHTML(err.Error()), b.studentsKeyboard(user))
		return
	}
	b.send(msg.Chat.ID, fmt.Sprintf("✅ %s теперь %s %s", escHTML(displayUser(target)), b.roleEmoji(role), b.roleName(role)), b.studentsKeyboard(user))
}

func normalizeRole(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "root", "староста":
		return "root"
	case "admin", "админ":
		return "admin"
	case "proforg", "профорг":
		return "proforg"
	case "student", "студент":
		return "student"
	default:
		return ""
	}
}
