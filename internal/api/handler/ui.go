package handler

import (
	"net/http"
	"strings"

	"github.com/iconidentify/xgrabba/pkg/ui"
)

// UIHandler serves the web UI.
type UIHandler struct{}

// NewUIHandler creates a new UI handler.
func NewUIHandler() *UIHandler {
	return &UIHandler{}
}

// Index serves the main UI page (full archive browser).
func (h *UIHandler) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(ui.IndexHTML)
}

// Quick serves the mobile-optimized quick archive page.
func (h *UIHandler) Quick(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(ui.QuickHTML)
}

// Smart serves the appropriate UI based on user agent (mobile vs desktop).
func (h *UIHandler) Smart(w http.ResponseWriter, r *http.Request) {
	ua := strings.ToLower(r.UserAgent())
	isMobile := strings.Contains(ua, "mobile") ||
		strings.Contains(ua, "android") ||
		strings.Contains(ua, "iphone") ||
		strings.Contains(ua, "ipad")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isMobile {
		w.Write(ui.QuickHTML)
	} else {
		w.Write(ui.IndexHTML)
	}
}

// AdminEvents serves the admin activity log page.
func (h *UIHandler) AdminEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(ui.AdminEventsHTML)
}

// Videos serves the dedicated video browser page.
func (h *UIHandler) Videos(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(ui.VideosHTML)
}

// Playlists serves the playlist management page.
func (h *UIHandler) Playlists(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(ui.PlaylistsHTML)
}
