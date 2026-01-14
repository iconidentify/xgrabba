package handler

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed ui/index.html
var uiHTML []byte

//go:embed ui/quick.html
var quickHTML []byte

//go:embed ui/admin_events.html
var adminEventsHTML []byte

// UIHandler serves the web UI.
type UIHandler struct{}

// NewUIHandler creates a new UI handler.
func NewUIHandler() *UIHandler {
	return &UIHandler{}
}

// Index serves the main UI page (full archive browser).
func (h *UIHandler) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(uiHTML)
}

// Quick serves the mobile-optimized quick archive page.
func (h *UIHandler) Quick(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(quickHTML)
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
		w.Write(quickHTML)
	} else {
		w.Write(uiHTML)
	}
}

// AdminEvents serves the admin activity log page.
func (h *UIHandler) AdminEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(adminEventsHTML)
}
