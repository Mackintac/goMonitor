package monitor

import (
	"context"
	"log"
	"sync"
	"time"
)

// Store is the interface the engine needs from the DB layer.
type Store interface {
	SaveResult(result CheckResult) error
	GetLastResult(monitorID int64) (*CheckResult, error)
	GetOpenIncident(monitorID int64) (*Incident, error)
	OpenIncident(monitorID int64) (*Incident, error)
	CloseIncident(incidentID int64) error
}

// Alerter is the interface for sending notifications.
type Alerter interface {
	SendDown(m Monitor, result CheckResult, incident Incident) error
	SendRecovered(m Monitor, incident Incident) error
}

// Engine manages all monitor goroutines.
type Engine struct {
	store    Store
	alerter  Alerter
	monitors []Monitor

	lastAlertAt map[int64]time.Time
	mu          sync.Mutex

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewEngine(store Store, alerter Alerter, monitors []Monitor) *Engine {
	return &Engine{
		store:       store,
		alerter:     alerter,
		monitors:    monitors,
		lastAlertAt: make(map[int64]time.Time),
	}
}

// Start launches one goroutine per enabled monitor.
func (e *Engine) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel

	for _, m := range e.monitors {
		if !m.Enabled {
			continue
		}
		e.wg.Add(1)
		go e.runMonitor(ctx, m)
	}

	log.Printf("engine started: %d monitor(s) active", len(e.monitors))
}

// Stop signals all goroutines to exit and waits for them to finish.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	log.Println("engine stopped")
}

// AddMonitor hot-adds a new monitor without restarting the engine.
func (e *Engine) AddMonitor(ctx context.Context, m Monitor) {
	e.mu.Lock()
	e.monitors = append(e.monitors, m)
	e.mu.Unlock()

	if m.Enabled {
		e.wg.Add(1)
		go e.runMonitor(ctx, m)
	}
}

// runMonitor is the per-monitor goroutine.
func (e *Engine) runMonitor(ctx context.Context, m Monitor) {
	defer e.wg.Done()

	// Check immediately on startup, don't wait for first tick
	e.tick(m)

	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] shutting down", m.Name)
			return
		case <-ticker.C:
			e.tick(m)
		}
	}
}

// tick runs one check cycle: check → store → incident → alert.
func (e *Engine) tick(m Monitor) {
	result := Check(m)

	if err := e.store.SaveResult(result); err != nil {
		log.Printf("[%s] failed to save result: %v", m.Name, err)
		return
	}

	status := "UP"
	if !result.Up {
		status = "DOWN"
	}
	log.Printf("[%s] %s %dms", m.Name, status, result.ResponseTime)

	e.handleIncident(m, result)
}

// handleIncident opens/closes incidents based on check result.
func (e *Engine) handleIncident(m Monitor, result CheckResult) {
	open, err := e.store.GetOpenIncident(m.ID)
	if err != nil {
		log.Printf("[%s] incident lookup error: %v", m.Name, err)
		return
	}

	if !result.Up {
		if open == nil {
			// New outage — open an incident
			incident, err := e.store.OpenIncident(m.ID)
			if err != nil {
				log.Printf("[%s] failed to open incident: %v", m.Name, err)
				return
			}
			log.Printf("[%s] incident opened", m.Name)
			e.maybeAlert(m, result, *incident, false)
		}
		// else: already know it's down, do nothing until recovery
	} else {
		if open != nil {
			// Recovery — close the incident
			if err := e.store.CloseIncident(open.ID); err != nil {
				log.Printf("[%s] failed to close incident: %v", m.Name, err)
				return
			}
			log.Printf("[%s] recovered after %s",
				m.Name, time.Since(open.StartedAt).Round(time.Second))
			e.maybeAlert(m, result, *open, true)
		}
	}
}

// maybeAlert fires an alert only if outside the cooldown window.
func (e *Engine) maybeAlert(m Monitor, result CheckResult, incident Incident, recovered bool) {
	if e.alerter == nil {
		return
	}

	e.mu.Lock()
	last := e.lastAlertAt[m.ID]
	e.mu.Unlock()

	if !recovered && time.Since(last) < m.AlertCooldown {
		log.Printf("[%s] alert suppressed (cooldown)", m.Name)
		return
	}

	var alertErr error
	if recovered {
		alertErr = e.alerter.SendRecovered(m, incident)
	} else {
		alertErr = e.alerter.SendDown(m, result, incident)
	}

	if alertErr != nil {
		log.Printf("[%s] alert failed: %v", m.Name, alertErr)
		return
	}

	e.mu.Lock()
	e.lastAlertAt[m.ID] = time.Now()
	e.mu.Unlock()
}
