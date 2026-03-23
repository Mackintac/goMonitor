package handler

import (
	"html/template"
	"log"
	"net/http"

	"github.com/Mackintac/goMonitor/internal/db"
	"github.com/Mackintac/goMonitor/internal/monitor"
)

type Handler struct {
	db   *db.DB
	tmpl *template.Template
}

func New(database *db.DB, tmpl *template.Template) *Handler {
	return &Handler{db: database, tmpl: tmpl}
}

func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("GET /", h.Dashboard)
}

type MonitorVM struct {
	Monitor monitor.Monitor
	Last    *monitor.CheckResult
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	monitors, err := h.db.GetAllMonitors()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	var items []MonitorVM
	for _, m := range monitors {
		last, _ := h.db.GetLastResult(m.ID)
		items = append(items, MonitorVM{Monitor: m, Last: last})
	}

	if err := h.tmpl.Execute(w, map[string]any{
		"Monitors": items,
	}); err != nil {
		log.Printf("template error: %v", err)
	}
}
