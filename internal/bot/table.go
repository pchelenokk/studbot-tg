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

// cmdTable sends the attendance workbook on demand.
// /table           → the whole workbook, one sheet per week
// /table 40        → only the sheet of ISO week 40
// /table 40 2026   → only week 40 of 2026
func (b *Bot) cmdTable(msg *tgbotapi.Message, user *storage.User) {
	if !b.canManage(user) {
		b.send(msg.Chat.ID, "❌ Таблицу посещаемости может запросить только староста или профорг", b.mainMenu(user))
		return
	}

	args := strings.Fields(msg.CommandArguments())
	year, week := 0, 0

	switch len(args) {
	case 0:
	case 1:
		w, err := strconv.Atoi(args[0])
		if err != nil || w < 1 || w > 53 {
			b.send(msg.Chat.ID, "❌ Номер недели — число от 1 до 53.\n\nФормат: <code>/table 40</code>", b.mainMenu(user))
			return
		}
		week = w
		year = currentISOYear()
	case 2:
		w, errW := strconv.Atoi(args[0])
		y, errY := strconv.Atoi(args[1])
		if errW != nil || errY != nil || w < 1 || w > 53 || y < 2020 || y > 2100 {
			b.send(msg.Chat.ID, "❌ Формат: <code>/table 40 2026</code> — неделя, затем год", b.mainMenu(user))
			return
		}
		week, year = w, y
	default:
		b.send(msg.Chat.ID, "❌ Слишком много аргументов.\n\nФормат: <code>/table</code> или <code>/table 40</code> или <code>/table 40 2026</code>", b.mainMenu(user))
		return
	}

	b.send(msg.Chat.ID, "⏳ Собираю таблицу…", nil)

	path, caption, err := b.buildTable(year, week)
	if err != nil {
		b.send(msg.Chat.ID, "❌ Не удалось собрать таблицу: "+escHTML(err.Error()), b.mainMenu(user))
		return
	}

	doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(path))
	doc.Caption = caption
	doc.ParseMode = "HTML"
	if _, err := b.api.Send(doc); err != nil {
		log.Printf("send table: %v", err)
		b.send(msg.Chat.ID, "❌ Не удалось отправить файл: "+escHTML(err.Error()), b.mainMenu(user))
	}
}

func currentISOYear() int {
	y, _ := time.Now().ISOWeek()
	return y
}

// buildTable writes the workbook and returns its path plus a caption. When a
// week is requested, only that week is exported.
func (b *Bot) buildTable(year, week int) (string, string, error) {
	rows, err := b.db.AllClosedCheckinVotes()
	if err != nil {
		return "", "", err
	}

	byWeek := map[[2]int][]storage.CheckinVoteRow{}
	seen := map[[2]int]bool{}

	for _, r := range rows {
		d, err := time.Parse("2006-01-02", r.Date)
		if err != nil {
			continue
		}
		y, w := d.ISOWeek()
		if week != 0 && (w != week || (year != 0 && y != year)) {
			continue
		}
		k := [2]int{y, w}
		byWeek[k] = append(byWeek[k], r)
		seen[k] = true
	}

	if week != 0 {
		// Keep the requested sheet even when nobody voted in it.
		y := year
		if y == 0 {
			y = currentISOYear()
		}
		seen[[2]int{y, week}] = true
	}

	if len(seen) == 0 {
		return "", "", fmt.Errorf("нет закрытых перекличек — таблица пока пустая")
	}

	keys := xlsx.SortWeekKeys(seen)
	header := []string{"Дата", "Предмет", "ФИО", "Telegram", "Статус", "Отметился в"}

	sheets := make([]xlsx.Sheet, 0, len(keys))
	total := 0
	for _, k := range keys {
		wr := byWeek[k]
		sort.SliceStable(wr, func(i, j int) bool {
			if wr[i].Date != wr[j].Date {
				return wr[i].Date < wr[j].Date
			}
			if wr[i].Subject != wr[j].Subject {
				return wr[i].Subject < wr[j].Subject
			}
			return wr[i].FullName < wr[j].FullName
		})

		sheet := xlsx.Sheet{
			Name:   xlsx.WeekSheetName(k[0], k[1]),
			Header: header,
			Widths: []float64{12, 22, 32, 22, 16, 20},
		}
		for _, r := range wr {
			telegram := "—"
			if r.Username != "" {
				telegram = "@" + r.Username
			}
			sheet.Rows = append(sheet.Rows, []string{
				r.Date, r.Subject, displayUserName(r.FullName, r.Username),
				telegram, statusLabel(r.Status), r.VotedAt,
			})
		}
		total += len(sheet.Rows)
		sheets = append(sheets, sheet)
	}

	if err := os.MkdirAll(b.cfg.ExportDir, 0o755); err != nil {
		return "", "", err
	}

	name := attendanceFilename
	caption := fmt.Sprintf("📊 <b>Посещаемость</b>\n\nЛистов: %d\nЗаписей: %d\n\nФормат: <code>/table 40</code> — только за неделю 40", len(sheets), total)

	if week != 0 {
		name = fmt.Sprintf("attendance_week%02d_%d.xlsx", week, year)
		caption = fmt.Sprintf("📊 <b>Посещаемость — %s</b>\n\nЗаписей: %d", xlsx.WeekSheetName(year, week), total)
	}

	path := filepath.Join(b.cfg.ExportDir, name)
	if err := xlsx.Write(path, sheets); err != nil {
		return "", "", err
	}
	return path, caption, nil
}

func (b *Bot) sendTable(chatID int64, msgID int, user *storage.User, arg string) {
	if !b.canManage(user) {
		b.toastFor(chatID, "❌ Нет прав")
		b.editOrSend(chatID, msgID, "❌ Недостаточно прав", b.mainMenu(user))
		return
	}
	b.editOrSend(chatID, msgID, "⏳ Собираю таблицу…", nil)
	path, caption, err := b.buildTable(0, 0)
	if err != nil {
		b.editOrSend(chatID, msgID, "❌ "+escHTML(err.Error()), b.mainMenu(user))
		return
	}
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
	doc.Caption = caption
	doc.ParseMode = "HTML"
	if _, err := b.api.Send(doc); err != nil {
		log.Printf("send table: %v", err)
	}
}
