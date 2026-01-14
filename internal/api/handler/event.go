package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/service"
)

// EventHandler handles event-related HTTP requests.
type EventHandler struct {
	eventSvc *service.EventService
	logger   *slog.Logger
}

// NewEventHandler creates a new event handler.
func NewEventHandler(eventSvc *service.EventService, logger *slog.Logger) *EventHandler {
	return &EventHandler{
		eventSvc: eventSvc,
		logger:   logger,
	}
}

// EventResponse represents an event in API responses.
type EventResponse struct {
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Severity  string          `json:"severity"`
	Category  string          `json:"category"`
	Message   string          `json:"message"`
	Source    string          `json:"source,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// EventListResponse contains paginated event list.
type EventListResponse struct {
	Events  []EventResponse `json:"events"`
	Total   int             `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	HasMore bool            `json:"has_more"`
}

// EventStatsResponse contains event service statistics.
type EventStatsResponse struct {
	Total          int                `json:"total"`
	BySeverity     map[string]int     `json:"by_severity"`
	BufferSize     int                `json:"buffer_size"`
	BufferUsed     int                `json:"buffer_used"`
	SSESubscribers int                `json:"sse_subscribers"`
	SQLiteEnabled  bool               `json:"sqlite_enabled"`
}

// List handles GET /api/v1/events
// Query parameters:
//   - severity: filter by severity (info, warning, error, success)
//   - category: filter by category (export, encryption, usb, bookmarks, ai, disk, tweet, network, system)
//   - source: filter by source component
//   - start_time: filter events after this time (RFC3339)
//   - end_time: filter events before this time (RFC3339)
//   - search: search in message text
//   - limit: max events to return (default 50, max 200)
//   - offset: pagination offset
//   - historical: if "true", query SQLite instead of ring buffer
func (h *EventHandler) List(w http.ResponseWriter, r *http.Request) {
	query := domain.EventQuery{
		Limit:  50,
		Offset: 0,
	}

	// Parse pagination
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			query.Limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			query.Offset = parsed
		}
	}

	// Parse filters
	if sev := r.URL.Query().Get("severity"); sev != "" {
		severity := domain.EventSeverity(sev)
		query.Filter.Severity = &severity
	}
	if cat := r.URL.Query().Get("category"); cat != "" {
		category := domain.EventCategory(cat)
		query.Filter.Category = &category
	}
	if src := r.URL.Query().Get("source"); src != "" {
		query.Filter.Source = src
	}
	if search := r.URL.Query().Get("search"); search != "" {
		query.Filter.SearchText = search
	}
	if startTime := r.URL.Query().Get("start_time"); startTime != "" {
		if t, err := time.Parse(time.RFC3339, startTime); err == nil {
			query.Filter.StartTime = &t
		}
	}
	if endTime := r.URL.Query().Get("end_time"); endTime != "" {
		if t, err := time.Parse(time.RFC3339, endTime); err == nil {
			query.Filter.EndTime = &t
		}
	}

	// Determine whether to query historical (SQLite) or recent (ring buffer)
	var result *domain.EventQueryResult
	var err error
	if r.URL.Query().Get("historical") == "true" {
		result, err = h.eventSvc.QueryHistorical(r.Context(), query)
	} else {
		result, err = h.eventSvc.Query(r.Context(), query)
	}

	if err != nil {
		h.logger.Error("failed to query events", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to query events")
		return
	}

	// Convert to response format
	response := EventListResponse{
		Events:  make([]EventResponse, 0, len(result.Events)),
		Total:   result.Total,
		Limit:   query.Limit,
		Offset:  query.Offset,
		HasMore: result.HasMore,
	}

	for _, e := range result.Events {
		response.Events = append(response.Events, EventResponse{
			ID:        string(e.ID),
			Timestamp: e.Timestamp,
			Severity:  string(e.Severity),
			Category:  string(e.Category),
			Message:   e.Message,
			Source:    e.Source,
			Metadata:  e.Metadata,
		})
	}

	h.writeJSON(w, http.StatusOK, response)
}

// RecentEventsResponse wraps the events array for the UI.
type RecentEventsResponse struct {
	Events []EventResponse `json:"events"`
}

// Recent handles GET /api/v1/events/recent
// Returns the most recent N events (default 50).
func (h *EventHandler) Recent(w http.ResponseWriter, r *http.Request) {
	n := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			n = parsed
		}
	}

	events := h.eventSvc.GetRecent(n)

	response := RecentEventsResponse{
		Events: make([]EventResponse, 0, len(events)),
	}
	for _, e := range events {
		response.Events = append(response.Events, EventResponse{
			ID:        string(e.ID),
			Timestamp: e.Timestamp,
			Severity:  string(e.Severity),
			Category:  string(e.Category),
			Message:   e.Message,
			Source:    e.Source,
			Metadata:  e.Metadata,
		})
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Stats handles GET /api/v1/events/stats
func (h *EventHandler) Stats(w http.ResponseWriter, r *http.Request) {
	stats := h.eventSvc.Stats()

	// Calculate severity counts from recent events
	events := h.eventSvc.GetRecent(1000) // Get up to 1000 recent events
	bySeverity := map[string]int{
		"info":    0,
		"warning": 0,
		"error":   0,
		"success": 0,
	}
	for _, e := range events {
		bySeverity[string(e.Severity)]++
	}

	h.writeJSON(w, http.StatusOK, EventStatsResponse{
		Total:          len(events),
		BySeverity:     bySeverity,
		BufferSize:     stats.BufferSize,
		BufferUsed:     stats.BufferUsed,
		SSESubscribers: stats.SSESubscribers,
		SQLiteEnabled:  stats.SQLiteEnabled,
	})
}

// Stream handles GET /api/v1/events/stream
// Server-Sent Events endpoint for real-time event streaming.
func (h *EventHandler) Stream(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher for SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Subscribe to events
	subID, eventCh := h.eventSvc.Subscribe()
	defer h.eventSvc.Unsubscribe(subID)

	h.logger.Info("SSE client connected", "subscriber_id", subID, "remote_addr", r.RemoteAddr)

	// Send initial connected event
	fmt.Fprintf(w, "event: connected\ndata: {\"subscriber_id\": %d}\n\n", subID)
	flusher.Flush()

	// Send keepalive every 30 seconds
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	// Stream events
	for {
		select {
		case <-r.Context().Done():
			h.logger.Info("SSE client disconnected", "subscriber_id", subID)
			return

		case event, ok := <-eventCh:
			if !ok {
				// Channel closed
				return
			}

			// Serialize event to JSON
			eventData, err := json.Marshal(EventResponse{
				ID:        string(event.ID),
				Timestamp: event.Timestamp,
				Severity:  string(event.Severity),
				Category:  string(event.Category),
				Message:   event.Message,
				Source:    event.Source,
				Metadata:  event.Metadata,
			})
			if err != nil {
				h.logger.Warn("failed to serialize event", "event_id", event.ID, "error", err)
				continue
			}

			// Send SSE event
			fmt.Fprintf(w, "event: event\ndata: %s\n\n", eventData)
			flusher.Flush()

		case <-keepalive.C:
			// Send keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// Categories handles GET /api/v1/events/categories
// Returns available event categories.
func (h *EventHandler) Categories(w http.ResponseWriter, r *http.Request) {
	categories := []string{
		string(domain.EventCategoryExport),
		string(domain.EventCategoryEncryption),
		string(domain.EventCategoryUSB),
		string(domain.EventCategoryBookmarks),
		string(domain.EventCategoryAI),
		string(domain.EventCategoryDisk),
		string(domain.EventCategoryTweet),
		string(domain.EventCategoryNetwork),
		string(domain.EventCategorySystem),
	}
	h.writeJSON(w, http.StatusOK, map[string][]string{"categories": categories})
}

// Severities handles GET /api/v1/events/severities
// Returns available event severities.
func (h *EventHandler) Severities(w http.ResponseWriter, r *http.Request) {
	severities := []string{
		string(domain.EventSeverityInfo),
		string(domain.EventSeverityWarning),
		string(domain.EventSeverityError),
		string(domain.EventSeveritySuccess),
	}
	h.writeJSON(w, http.StatusOK, map[string][]string{"severities": severities})
}

func (h *EventHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *EventHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
