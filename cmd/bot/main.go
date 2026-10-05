package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"studbot/internal/admin"
	"studbot/internal/bot"
	"studbot/internal/config"
	"studbot/internal/storage"
	"studbot/internal/webapp"
)

const reconnectDelay = 15 * time.Second

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting studbot...")

	cfg := config.Load()
	log.Printf("Config loaded: ListenAddr=%s, DBPath=%s, AdminAddr=%s", cfg.ListenAddr, cfg.DBPath, cfg.AdminAddr)

	if cfg.Token == "" {
		log.Fatal("BOT_TOKEN is required")
	}
	if cfg.AdminPassword == "" {
		log.Println("WARNING: ADMIN_PASSWORD is empty — the admin panel accepts localhost only")
	}

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatalf("create upload dir: %v", err)
	}

	db, err := storage.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}
	defer db.Close()
	log.Println("Storage initialized")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// HTTP servers start first and work without Telegram: the mini app and the
	// admin panel stay available even when the bot is offline.
	bots := bot.NewProvider()

	webApp := webapp.New(cfg, db, bots)
	panel := admin.New(cfg, db, bots)
	panel.Routes()

	go func() {
		log.Println("WebApp server starting...")
		if err := webApp.Run(); err != nil {
			log.Printf("webapp stopped: %v", err)
			stop()
		}
	}()

	go func() {
		if err := panel.Run(cfg.AdminAddr); err != nil {
			log.Printf("admin panel stopped: %v", err)
			stop()
		}
	}()

	go runBot(ctx, cfg, db, bots, panel)

	panel.AddLog("панель управления запущена на http://" + cfg.AdminAddr)
	log.Println("Studbot is running. Press Ctrl+C to stop.")
	<-ctx.Done()

	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := webApp.Shutdown(shutdownCtx); err != nil {
		log.Printf("webapp shutdown: %v", err)
	}
	if err := panel.Shutdown(shutdownCtx); err != nil {
		log.Printf("admin shutdown: %v", err)
	}
	log.Println("Stopped")
}

// runBot connects to Telegram and reconnects forever. Telegram can be blocked
// or unreachable at startup, so the rest of the service must not depend on it.
func runBot(ctx context.Context, cfg *config.Config, db *storage.DB, bots *bot.Provider, panel *admin.Server) {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}

		b, err := bot.New(cfg, db)
		if err != nil {
			log.Printf("bot offline (attempt %d): %v — retrying in %s", attempt, err, reconnectDelay)
			panel.AddLog("бот не в сети: " + err.Error())
			if !sleepCtx(ctx, reconnectDelay) {
				return
			}
			continue
		}

		bots.Set(b)
		name := b.SelfName()
		log.Printf("Bot @%s started, waiting for updates...", name)
		panel.AddLog("бот @" + name + " запущен")

		if n := b.RecoverInterruptedCheckins(); n > 0 {
			panel.AddLog(fmt.Sprintf("закрыто зависших перекличек после перезапуска: %d", n))
		}

		err = b.Run(ctx)
		bots.Set(nil)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("bot stopped: %v — reconnecting in %s", err, reconnectDelay)
		} else {
			log.Printf("bot stopped, reconnecting in %s", reconnectDelay)
		}
		if !sleepCtx(ctx, reconnectDelay) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
