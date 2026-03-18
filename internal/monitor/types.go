package monitor

import "time"

type MonitorType string

const (
	TypeHTTP MonitorType = "http"
	TypeTCP  MonitorType = "tcp"
)

type Monitor struct {
	ID            int64
	Name          string
	URL           string
	Type          MonitorType
	Interval      time.Duration
	Timeout       time.Duration
	Enabled       bool
	AlertEmail    string
	AlertWebhook  string
	AlertCooldown time.Duration
}

type CheckResult struct {
	ID           int64
	MonitorID    int64
	CheckedAt    time.Time
	StatusCode   int
	ResponseTime int64
	Up           bool
	Error        string
}

type Incident struct {
	ID         int64
	MonitorID  int64
	StartedAt  time.Time
	ResolvedAt *time.Time
	AlertSent  bool
}

type UptimeStats struct {
	MonitorID     int64
	WindowDays    int
	TotalChecks   int
	UpChecks      int
	UptimePct     float64
	AvgResponseMs float64
}
