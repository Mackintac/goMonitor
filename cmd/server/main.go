package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mackintac/goMonitor/internal/alert"
	"github.com/Mackintac/goMonitor/internal/db"
	"github.com/Mackintac/goMonitor/internal/monitor"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	// ---- Database ----
	dbPath := envOr("DB_PATH", "uptime.db")
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// ---- Seed demo monitors ----
	monitors, err := database.GetAllMonitors()
	if err != nil {
		log.Fatalf("load monitors: %v", err)
	}
	if len(monitors) == 0 {
		log.Println("no monitors found, seeding defaults...")
		seeds := []monitor.Monitor{
			{
				Name:          "Google",
				URL:           "https://www.google.com",
				Type:          monitor.TypeHTTP,
				Interval:      30 * time.Second,
				Timeout:       10 * time.Second,
				Enabled:       true,
				AlertCooldown: 15 * time.Minute,
			},
			{
				Name:          "Cloudflare",
				URL:           "https://cloudflare.com",
				Type:          monitor.TypeHTTP,
				Interval:      30 * time.Second,
				Timeout:       10 * time.Second,
				Enabled:       true,
				AlertCooldown: 15 * time.Minute,
			},
			{
				Name:          "DNS (TCP)",
				URL:           "8.8.8.8:53",
				Type:          monitor.TypeTCP,
				Interval:      30 * time.Second,
				Timeout:       5 * time.Second,
				Enabled:       true,
				AlertCooldown: 15 * time.Minute,
			},
		}
		for _, m := range seeds {
			id, err := database.CreateMonitor(m)
			if err != nil {
				log.Fatalf("seed monitor: %v", err)
			}
			m.ID = id
			monitors = append(monitors, m)
		}
	}

	// ---- Alerter ----
	var alerter monitor.Alerter
	if os.Getenv("SMTP_HOST") != "" {
		alerter = alert.New(alert.Config{
			SMTPHost: os.Getenv("SMTP_HOST"),
			SMTPPort: 587,
			SMTPUser: os.Getenv("SMTP_USER"),
			SMTPPass: os.Getenv("SMTP_PASS"),
			SMTPFrom: envOr("SMTP_FROM", os.Getenv("SMTP_USER")),
		})
		log.Printf("SMTP alerting enabled via %s", os.Getenv("SMTP_HOST"))
	} else {
		alerter = &alert.NoopAlerter{}
		log.Println("no SMTP configured, using noop alerter")
	}

	// ---- Engine ----
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := monitor.NewEngine(database, alerter, monitors)
	engine.Start(ctx)

	// ---- Graceful shutdown ----
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	cancel()
	engine.Stop()
	log.Println("bye")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
