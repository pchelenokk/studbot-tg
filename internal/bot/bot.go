package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"studbot/internal/config"
	"studbot/internal/storage"
	"studbot/internal/telegram"
)

// Provider hands the current bot instance to the HTTP servers. Telegram may be
// unreachable when the process starts, so the servers must run without a bot
// and pick it up as soon as it comes online.
type Provider struct{ ptr atomic.Pointer[Bot] }

func NewProvider() *Provider { return &Provider{} }

func (p *Provider) Set(b *Bot) { p.ptr.Store(b) }

func (p *Provider) Get() *Bot { return p.ptr.Load() }

type Bot struct {
	cfg    *config.Config
	db     *storage.DB
	tg     *telegram.Client
	api    *tgbotapi.BotAPI
	rootes map[int64]string

	// mu guards wizards and userLocks. Per-user state (Wizard) is mutated
	// only while the matching userLocks entry is held.
	mu        sync.Mutex
	wizards   map[int64]*Wizard
	userLocks map[int64]*sync.Mutex
}

func New(cfg *config.Config, db *storage.DB) (*Bot, error) {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	if cfg.ProxyURL != "" {
		if u, err := url.Parse(cfg.ProxyURL); err == nil {
			httpClient.Transport = &http.Transport{
				Proxy:               http.ProxyURL(u),
				TLSHandshakeTimeout: 15 * time.Second,
			}
			log.Printf("Telegram proxy: %s", cfg.ProxyURL)
		}
	}

	api, err := tgbotapi.NewBotAPIWithClient(cfg.Token, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}

	rootes := make(map[int64]string)
	for _, id := range cfg.RootUserIDs {
		rootes[id] = "root"
	}
	for _, id := range cfg.AdminUserIDs {
		rootes[id] = "admin"
	}
	for _, id := range cfg.ProfOrgUserIDs {
		if _, exists := rootes[id]; !exists {
			rootes[id] = "proforg"
		}
	}

	return &Bot{
		cfg:       cfg,
		db:        db,
		tg:        telegram.NewClientWithProxy(cfg.Token, cfg.ProxyURL),
		api:       api,
		rootes:    rootes,
		wizards:   make(map[int64]*Wizard),
		userLocks: make(map[int64]*sync.Mutex),
	}, nil
}

// userLock returns the per-user mutex, creating it on first use. Updates of a
// single Telegram user are serialized with it, so wizard state cannot be
// corrupted by two goroutines handling two rapid messages at once.
func (b *Bot) userLock(tgID int64) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.userLocks[tgID]
	if m == nil {
		m = &sync.Mutex{}
		b.userLocks[tgID] = m
	}
	return m
}

func (b *Bot) getWizard(tgID int64) *Wizard {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wizards[tgID]
}

func (b *Bot) putWizard(tgID int64, w *Wizard) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wizards[tgID] = w
}

func (b *Bot) delWizard(tgID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.wizards, tgID)
}

func (b *Bot) Run(ctx context.Context) error {
	botUser, err := b.api.GetMe()
	if err != nil {
		return fmt.Errorf("getMe: %w", err)
	}
	log.Printf("Bot @%s online", botUser.UserName)

	if err := b.SetupMenu(); err != nil {
		log.Printf("menu setup: %v", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query"}

	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			go b.handleUpdate(update)
		}
	}
}

func (b *Bot) SetupMenu() error {
	if b.cfg.WebAppURL != "" {
		if err := b.tg.SetChatMenuButton(0, "Приложение", b.cfg.WebAppURL); err != nil {
			log.Printf("menu button: %v", err)
		}
	}
	commands := []telegram.BotCommand{
		{Command: "start", Description: "Меню бота"},
		{Command: "help", Description: "Список команд"},
		{Command: "books", Description: "Библиотека"},
		{Command: "homework", Description: "Домашние задания"},
		{Command: "mark", Description: "Отметка посещаемости"},
		{Command: "polls", Description: "Опросы"},
		{Command: "events", Description: "События"},
		{Command: "stats", Description: "Статистика"},
		{Command: "cancel", Description: "Отменить текущее действие"},
	}
	return b.tg.SetMyCommands(commands)
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		lock := b.userLock(update.CallbackQuery.From.ID)
		lock.Lock()
		defer lock.Unlock()
		b.handleCallback(update.CallbackQuery)
		return
	}
	msg := update.Message
	if msg == nil || msg.From == nil {
		return
	}

	lock := b.userLock(msg.From.ID)
	lock.Lock()
	defer lock.Unlock()

	user, err := b.db.UpsertUser(msg.From.ID, msg.From.UserName, displayName(msg.From.FirstName, msg.From.LastName))
	if err != nil {
		log.Printf("upsert user: %v", err)
		return
	}
	b.applyConfiguredRole(user)

	if msg.IsCommand() {
		b.dropWizard(msg.From.ID)
		b.handleCommand(msg, user)
		return
	}

	// Files are checked first: a document/photo arrives without text, so the
	// wizard text handler must not consume the step before the file is seen.
	if b.handleBookFile(msg, user) {
		return
	}

	if b.handleWizardText(msg, user) {
		return
	}

	b.send(msg.Chat.ID, "Используй кнопки ниже или /help", b.mainMenu(user))
}

func displayName(first, last string) string {
	if last == "" {
		return first
	}
	if first == "" {
		return last
	}
	return first + " " + last
}

func (b *Bot) applyConfiguredRole(user *storage.User) {
	role, ok := b.rootes[user.TgID]
	if !ok || user.Role == role {
		return
	}
	if err := b.db.SetRole(user.TgID, role); err != nil {
		log.Printf("set role %d: %v", user.TgID, err)
		return
	}
	user.Role = role
	log.Printf("role assigned: %d -> %s", user.TgID, role)
}

func (b *Bot) canManage(user *storage.User) bool {
	return user.Role == "root" || user.Role == "admin" || user.Role == "proforg"
}

func (b *Bot) isRoot(user *storage.User) bool {
	return user.Role == "root" || user.Role == "admin"
}

func asReplyMarkup(markup interface{}) *tgbotapi.InlineKeyboardMarkup {
	switch v := markup.(type) {
	case tgbotapi.InlineKeyboardMarkup:
		return &v
	case *tgbotapi.InlineKeyboardMarkup:
		return v
	default:
		return nil
	}
}

func (b *Bot) send(chatID int64, text string, markup interface{}) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = asReplyMarkup(markup)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send: %v", err)
	}
}

func (b *Bot) sendDoc(chatID int64, fileID, caption string) {
	msg := tgbotapi.NewDocument(chatID, tgbotapi.FileID(fileID))
	msg.Caption = caption
	msg.ParseMode = "HTML"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send doc: %v", err)
	}
}

func (b *Bot) sendPhoto(chatID int64, fileID, caption string) {
	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(fileID))
	msg.Caption = caption
	msg.ParseMode = "HTML"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send photo: %v", err)
	}
}

func (b *Bot) editOrSend(chatID int64, messageID int, text string, markup interface{}) {
	reply := asReplyMarkup(markup)
	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
		edit.ParseMode = "HTML"
		edit.ReplyMarkup = reply
		if _, err := b.api.Send(edit); err == nil {
			return
		}
	}
	b.send(chatID, text, reply)
}

func (b *Bot) roleEmoji(role string) string {
	switch role {
	case "root", "admin":
		return "👑"
	case "proforg":
		return "📋"
	default:
		return "👤"
	}
}

func (b *Bot) roleName(role string) string {
	switch role {
	case "root":
		return "Староста"
	case "admin":
		return "Админ"
	case "proforg":
		return "Профорг"
	default:
		return "Студент"
	}
}

func (b *Bot) SelfName() string {
	return b.api.Self.UserName
}

func (b *Bot) SendText(chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) GetMe() (tgbotapi.User, error) {
	return b.api.GetMe()
}

func (b *Bot) GetFilePath(fileID string) (string, error) {
	return b.tg.GetFile(fileID)
}

func (b *Bot) DownloadFile(filePath, destDir, destName string) (string, error) {
	return b.tg.DownloadFile(filePath, destDir, destName)
}
