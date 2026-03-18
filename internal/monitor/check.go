package monitor

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// Check dispatches to the correct checker based on monitor type.
func Check(m Monitor) CheckResult {
	switch m.Type {
	case TypeTCP:
		return checkTCP(m)
	default:
		return checkHTTP(m)
	}
}

func checkHTTP(m Monitor) CheckResult {
	start := time.Now()
	result := CheckResult{
		MonitorID: m.ID,
		CheckedAt: start,
	}

	client := &http.Client{
		Timeout: m.Timeout,
		// Don't follow redirects — we want the real status code
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(m.URL)
	result.ResponseTime = time.Since(start).Milliseconds()

	if err != nil {
		result.Up = false
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Up = resp.StatusCode >= 200 && resp.StatusCode < 400
	if !result.Up {
		result.Error = fmt.Sprintf("unexpected status code: %d", resp.StatusCode)
	}
	return result
}

func checkTCP(m Monitor) CheckResult {
	start := time.Now()
	result := CheckResult{
		MonitorID: m.ID,
		CheckedAt: start,
	}

	conn, err := net.DialTimeout("tcp", m.URL, m.Timeout)
	result.ResponseTime = time.Since(start).Milliseconds()

	if err != nil {
		result.Up = false
		result.Error = err.Error()
		return result
	}
	conn.Close()
	result.Up = true
	return result
}
