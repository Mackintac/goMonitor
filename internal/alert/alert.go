package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"time"

	"github.com/Mackintac/goMonitor/internal/monitor"
)

// Config holds alerting credentials, loaded from env vars.
type Config struct {
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
}

// Alerter sends real notifications.
type Alerter struct {
	cfg Config
}

func New(cfg Config) *Alerter {
	return &Alerter{cfg: cfg}
}

func (a *Alerter) SendDown(m monitor.Monitor, result monitor.CheckResult, incident monitor.Incident) error {
	subject := fmt.Sprintf("🔴 DOWN: %s is unreachable", m.Name)
	body := fmt.Sprintf(
		"Monitor: %s\nURL: %s\nError: %s\nTime: %s\nIncident started: %s",
		m.Name, m.URL, result.Error,
		result.CheckedAt.Format(time.RFC1123),
		incident.StartedAt.Format(time.RFC1123),
	)
	return a.send(m, subject, body)
}

func (a *Alerter) SendRecovered(m monitor.Monitor, incident monitor.Incident) error {
	duration := time.Since(incident.StartedAt).Round(time.Second)
	subject := fmt.Sprintf("✅ RECOVERED: %s is back up", m.Name)
	body := fmt.Sprintf(
		"Monitor: %s\nURL: %s\nOutage duration: %s\nRecovered at: %s",
		m.Name, m.URL, duration,
		time.Now().Format(time.RFC1123),
	)
	return a.send(m, subject, body)
}

func (a *Alerter) send(m monitor.Monitor, subject, body string) error {
	var errs []string

	if m.AlertEmail != "" {
		if err := a.sendEmail(m.AlertEmail, subject, body); err != nil {
			errs = append(errs, "email: "+err.Error())
		}
	}
	if m.AlertWebhook != "" {
		if err := sendWebhook(m.AlertWebhook, subject+"\n"+body); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("alert errors: %v", errs)
	}
	return nil
}

func (a *Alerter) sendEmail(to, subject, body string) error {
	if a.cfg.SMTPHost == "" {
		log.Printf("[alert][email] no SMTP configured, skipping")
		return nil
	}

	addr := fmt.Sprintf("%s:%d", a.cfg.SMTPHost, a.cfg.SMTPPort)
	auth := smtp.PlainAuth("", a.cfg.SMTPUser, a.cfg.SMTPPass, a.cfg.SMTPHost)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		a.cfg.SMTPFrom, to, subject, body)

	return smtp.SendMail(addr, auth, a.cfg.SMTPFrom, []string{to}, []byte(msg))
}

func sendWebhook(url, text string) error {
	payload, _ := json.Marshal(map[string]string{"text": text})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// NoopAlerter logs alerts but sends nothing. Used in development.
type NoopAlerter struct{}

func (n *NoopAlerter) SendDown(m monitor.Monitor, result monitor.CheckResult, incident monitor.Incident) error {
	log.Printf("[noop-alert] DOWN: %s — %s", m.Name, result.Error)
	return nil
}

func (n *NoopAlerter) SendRecovered(m monitor.Monitor, incident monitor.Incident) error {
	log.Printf("[noop-alert] RECOVERED: %s (was down %s)",
		m.Name, time.Since(incident.StartedAt).Round(time.Second))
	return nil
}
