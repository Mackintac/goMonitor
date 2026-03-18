package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Mackintac/goMonitor/internal/monitor"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := conn.Exec(p); err != nil {
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS monitors (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT    NOT NULL,
		url           TEXT    NOT NULL,
		type          TEXT    NOT NULL DEFAULT 'http',
		interval_sec  INTEGER NOT NULL DEFAULT 60,
		timeout_sec   INTEGER NOT NULL DEFAULT 10,
		enabled       INTEGER NOT NULL DEFAULT 1,
		alert_email   TEXT    NOT NULL DEFAULT '',
		alert_webhook TEXT    NOT NULL DEFAULT '',
		cooldown_sec  INTEGER NOT NULL DEFAULT 900,
		created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS check_results (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		checked_at  TEXT    NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		response_ms INTEGER NOT NULL DEFAULT 0,
		up          INTEGER NOT NULL DEFAULT 0,
		error       TEXT    NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_results_monitor_time
		ON check_results(monitor_id, checked_at DESC);

	CREATE TABLE IF NOT EXISTS incidents (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		started_at  TEXT    NOT NULL,
		resolved_at TEXT,
		alert_sent  INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_incidents_monitor
		ON incidents(monitor_id, resolved_at);
	`
	_, err := db.conn.Exec(schema)
	return err
}

// ---- Monitor CRUD ----

func (db *DB) CreateMonitor(m monitor.Monitor) (int64, error) {
	res, err := db.conn.Exec(`
		INSERT INTO monitors
			(name, url, type, interval_sec, timeout_sec, enabled, alert_email, alert_webhook, cooldown_sec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.URL, string(m.Type),
		int(m.Interval.Seconds()), int(m.Timeout.Seconds()),
		boolToInt(m.Enabled),
		m.AlertEmail, m.AlertWebhook,
		int(m.AlertCooldown.Seconds()),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetAllMonitors() ([]monitor.Monitor, error) {
	rows, err := db.conn.Query(`
		SELECT id, name, url, type, interval_sec, timeout_sec, enabled,
		       alert_email, alert_webhook, cooldown_sec
		FROM monitors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []monitor.Monitor
	for rows.Next() {
		var m monitor.Monitor
		var intervalSec, timeoutSec, cooldownSec int
		var mType string
		var enabled int
		if err := rows.Scan(
			&m.ID, &m.Name, &m.URL, &mType,
			&intervalSec, &timeoutSec, &enabled,
			&m.AlertEmail, &m.AlertWebhook, &cooldownSec,
		); err != nil {
			return nil, err
		}
		m.Type = monitor.MonitorType(mType)
		m.Interval = time.Duration(intervalSec) * time.Second
		m.Timeout = time.Duration(timeoutSec) * time.Second
		m.AlertCooldown = time.Duration(cooldownSec) * time.Second
		m.Enabled = enabled == 1
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

func (db *DB) DeleteMonitor(id int64) error {
	_, err := db.conn.Exec("DELETE FROM monitors WHERE id = ?", id)
	return err
}

// ---- Store interface methods (used by engine) ----

func (db *DB) SaveResult(r monitor.CheckResult) error {
	_, err := db.conn.Exec(`
		INSERT INTO check_results (monitor_id, checked_at, status_code, response_ms, up, error)
		VALUES (?, ?, ?, ?, ?, ?)`,
		r.MonitorID,
		r.CheckedAt.UTC().Format(time.RFC3339),
		r.StatusCode,
		r.ResponseTime,
		boolToInt(r.Up),
		r.Error,
	)
	return err
}

func (db *DB) GetLastResult(monitorID int64) (*monitor.CheckResult, error) {
	row := db.conn.QueryRow(`
		SELECT id, monitor_id, checked_at, status_code, response_ms, up, error
		FROM check_results
		WHERE monitor_id = ?
		ORDER BY checked_at DESC LIMIT 1`, monitorID)
	return scanResult(row)
}

func (db *DB) GetOpenIncident(monitorID int64) (*monitor.Incident, error) {
	row := db.conn.QueryRow(`
		SELECT id, monitor_id, started_at, resolved_at, alert_sent
		FROM incidents
		WHERE monitor_id = ? AND resolved_at IS NULL
		ORDER BY started_at DESC LIMIT 1`, monitorID)
	return scanIncident(row)
}

func (db *DB) OpenIncident(monitorID int64) (*monitor.Incident, error) {
	now := time.Now().UTC()
	res, err := db.conn.Exec(`
		INSERT INTO incidents (monitor_id, started_at) VALUES (?, ?)`,
		monitorID, now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &monitor.Incident{
		ID:        id,
		MonitorID: monitorID,
		StartedAt: now,
	}, nil
}

func (db *DB) CloseIncident(incidentID int64) error {
	_, err := db.conn.Exec(`
		UPDATE incidents SET resolved_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), incidentID)
	return err
}

func (db *DB) GetRecentResults(monitorID int64, limit int) ([]monitor.CheckResult, error) {
	rows, err := db.conn.Query(`
		SELECT id, monitor_id, checked_at, status_code, response_ms, up, error
		FROM check_results
		WHERE monitor_id = ?
		ORDER BY checked_at DESC LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []monitor.CheckResult
	for rows.Next() {
		r, err := scanResultRow(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (db *DB) GetUptimeStats(monitorID int64, days int) (monitor.UptimeStats, error) {
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	row := db.conn.QueryRow(`
		SELECT
			COUNT(*),
			SUM(CASE WHEN up=1 THEN 1 ELSE 0 END),
			AVG(response_ms)
		FROM check_results
		WHERE monitor_id = ? AND checked_at >= ?`, monitorID, since)

	var stats monitor.UptimeStats
	stats.MonitorID = monitorID
	stats.WindowDays = days

	var avgMs sql.NullFloat64
	if err := row.Scan(&stats.TotalChecks, &stats.UpChecks, &avgMs); err != nil {
		return stats, err
	}
	if stats.TotalChecks > 0 {
		stats.UptimePct = float64(stats.UpChecks) / float64(stats.TotalChecks) * 100
	}
	if avgMs.Valid {
		stats.AvgResponseMs = avgMs.Float64
	}
	return stats, nil
}

// ---- helpers ----

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanResult(row *sql.Row) (*monitor.CheckResult, error) {
	var r monitor.CheckResult
	var checkedAt string
	var up int
	err := row.Scan(&r.ID, &r.MonitorID, &checkedAt, &r.StatusCode, &r.ResponseTime, &up, &r.Error)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CheckedAt, _ = time.Parse(time.RFC3339, checkedAt)
	r.Up = up == 1
	return &r, nil
}

func scanResultRow(rows *sql.Rows) (monitor.CheckResult, error) {
	var r monitor.CheckResult
	var checkedAt string
	var up int
	err := rows.Scan(&r.ID, &r.MonitorID, &checkedAt, &r.StatusCode, &r.ResponseTime, &up, &r.Error)
	if err != nil {
		return r, err
	}
	r.CheckedAt, _ = time.Parse(time.RFC3339, checkedAt)
	r.Up = up == 1
	return r, nil
}

func scanIncident(row *sql.Row) (*monitor.Incident, error) {
	var inc monitor.Incident
	var startedAt string
	var resolvedAt sql.NullString
	var alertSent int
	err := row.Scan(&inc.ID, &inc.MonitorID, &startedAt, &resolvedAt, &alertSent)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inc.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if resolvedAt.Valid {
		t, _ := time.Parse(time.RFC3339, resolvedAt.String)
		inc.ResolvedAt = &t
	}
	inc.AlertSent = alertSent == 1
	return &inc, nil
}
