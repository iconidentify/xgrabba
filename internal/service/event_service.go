package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// EventServiceConfig configures the event service.
type EventServiceConfig struct {
	// RingBufferSize is the number of events to keep in memory.
	// Default: 1000
	RingBufferSize int

	// PersistToSQLite enables SQLite persistence for historical events.
	PersistToSQLite bool

	// SQLitePath is the path to the SQLite database file.
	SQLitePath string

	// RetentionDays is how long to keep events in SQLite (0 = forever).
	RetentionDays int
}

// DefaultEventServiceConfig returns sensible defaults.
func DefaultEventServiceConfig() EventServiceConfig {
	return EventServiceConfig{
		RingBufferSize:  1000,
		PersistToSQLite: false,
		RetentionDays:   30,
	}
}

// EventService manages system events with an in-memory ring buffer
// and optional SQLite persistence.
type EventService struct {
	cfg    EventServiceConfig
	logger *slog.Logger

	// Ring buffer for recent events
	mu       sync.RWMutex
	events   []domain.Event
	head     int // Next write position
	count    int // Number of events in buffer
	eventSeq uint64 // Monotonic sequence for event IDs

	// SQLite persistence (optional)
	db *sql.DB

	// SSE subscribers for real-time streaming
	subMu       sync.RWMutex
	subscribers map[uint64]chan domain.Event
	subSeq      uint64
}

// NewEventService creates a new event service.
func NewEventService(cfg EventServiceConfig, logger *slog.Logger) (*EventService, error) {
	if cfg.RingBufferSize <= 0 {
		cfg.RingBufferSize = 1000
	}

	svc := &EventService{
		cfg:         cfg,
		logger:      logger,
		events:      make([]domain.Event, cfg.RingBufferSize),
		subscribers: make(map[uint64]chan domain.Event),
	}

	// Initialize SQLite if enabled
	if cfg.PersistToSQLite && cfg.SQLitePath != "" {
		if err := svc.initSQLite(); err != nil {
			return nil, fmt.Errorf("init sqlite: %w", err)
		}
		logger.Info("event persistence enabled", "path", cfg.SQLitePath)
	}

	return svc, nil
}

// initSQLite initializes the SQLite database.
func (s *EventService) initSQLite() error {
	db, err := sql.Open("sqlite3", s.cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	// Create events table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			timestamp DATETIME NOT NULL,
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			message TEXT NOT NULL,
			source TEXT,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_events_severity ON events(severity);
		CREATE INDEX IF NOT EXISTS idx_events_category ON events(category);
	`)
	if err != nil {
		db.Close()
		return fmt.Errorf("create table: %w", err)
	}

	s.db = db
	return nil
}

// Close closes the event service and any open resources.
func (s *EventService) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Emit records an event to the event log.
func (s *EventService) Emit(event domain.Event) {
	// Generate ID if not set
	if event.ID == "" {
		seq := atomic.AddUint64(&s.eventSeq, 1)
		event.ID = domain.EventID(fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), seq))
	}

	// Set timestamp if not set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Add to ring buffer
	s.mu.Lock()
	s.events[s.head] = event
	s.head = (s.head + 1) % s.cfg.RingBufferSize
	if s.count < s.cfg.RingBufferSize {
		s.count++
	}
	s.mu.Unlock()

	// Persist to SQLite if enabled
	if s.db != nil {
		go s.persistEvent(event)
	}

	// Notify SSE subscribers
	s.notifySubscribers(event)

	// Log the event
	logLevel := slog.LevelInfo
	switch event.Severity {
	case domain.EventSeverityWarning:
		logLevel = slog.LevelWarn
	case domain.EventSeverityError:
		logLevel = slog.LevelError
	}
	s.logger.Log(context.Background(), logLevel, "event emitted",
		"event_id", event.ID,
		"category", event.Category,
		"severity", event.Severity,
		"message", event.Message,
		"source", event.Source,
	)
}

// EmitInfo is a convenience method for info-level events.
func (s *EventService) EmitInfo(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
	s.Emit(domain.Event{
		Severity: domain.EventSeverityInfo,
		Category: category,
		Source:   source,
		Message:  message,
		Metadata: metadata.ToJSON(),
	})
}

// EmitWarning is a convenience method for warning-level events.
func (s *EventService) EmitWarning(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
	s.Emit(domain.Event{
		Severity: domain.EventSeverityWarning,
		Category: category,
		Source:   source,
		Message:  message,
		Metadata: metadata.ToJSON(),
	})
}

// EmitError is a convenience method for error-level events.
func (s *EventService) EmitError(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
	s.Emit(domain.Event{
		Severity: domain.EventSeverityError,
		Category: category,
		Source:   source,
		Message:  message,
		Metadata: metadata.ToJSON(),
	})
}

// EmitSuccess is a convenience method for success-level events.
func (s *EventService) EmitSuccess(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
	s.Emit(domain.Event{
		Severity: domain.EventSeveritySuccess,
		Category: category,
		Source:   source,
		Message:  message,
		Metadata: metadata.ToJSON(),
	})
}

// persistEvent saves an event to SQLite.
func (s *EventService) persistEvent(event domain.Event) {
	if s.db == nil {
		return
	}

	metadataStr := ""
	if event.Metadata != nil {
		metadataStr = string(event.Metadata)
	}

	_, err := s.db.Exec(`
		INSERT INTO events (id, timestamp, severity, category, message, source, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.Timestamp, event.Severity, event.Category, event.Message, event.Source, metadataStr)

	if err != nil {
		s.logger.Warn("failed to persist event", "event_id", event.ID, "error", err)
	}
}

// Query returns events matching the filter with pagination.
func (s *EventService) Query(ctx context.Context, query domain.EventQuery) (*domain.EventQueryResult, error) {
	// Default pagination
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	// Query from ring buffer (most recent events)
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect all events from ring buffer in reverse chronological order
	allEvents := make([]domain.Event, 0, s.count)
	for i := 0; i < s.count; i++ {
		// Read backwards from head-1
		idx := (s.head - 1 - i + s.cfg.RingBufferSize) % s.cfg.RingBufferSize
		event := s.events[idx]
		if event.ID == "" {
			continue // Empty slot
		}
		if s.matchesFilter(event, query.Filter) {
			allEvents = append(allEvents, event)
		}
	}

	// Apply pagination
	total := len(allEvents)
	start := query.Offset
	if start >= total {
		return &domain.EventQueryResult{
			Events:  []domain.Event{},
			Total:   total,
			HasMore: false,
		}, nil
	}

	end := start + query.Limit
	if end > total {
		end = total
	}

	return &domain.EventQueryResult{
		Events:  allEvents[start:end],
		Total:   total,
		HasMore: end < total,
	}, nil
}

// QueryHistorical queries events from SQLite storage.
func (s *EventService) QueryHistorical(ctx context.Context, query domain.EventQuery) (*domain.EventQueryResult, error) {
	if s.db == nil {
		return &domain.EventQueryResult{Events: []domain.Event{}}, nil
	}

	// Default pagination
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	// Build query
	var conditions []string
	var args []interface{}

	if query.Filter.Severity != nil {
		conditions = append(conditions, "severity = ?")
		args = append(args, *query.Filter.Severity)
	}
	if query.Filter.Category != nil {
		conditions = append(conditions, "category = ?")
		args = append(args, *query.Filter.Category)
	}
	if query.Filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, query.Filter.Source)
	}
	if query.Filter.StartTime != nil {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, *query.Filter.StartTime)
	}
	if query.Filter.EndTime != nil {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, *query.Filter.EndTime)
	}
	if query.Filter.SearchText != "" {
		conditions = append(conditions, "message LIKE ?")
		args = append(args, "%"+query.Filter.SearchText+"%")
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM events %s", whereClause)
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count events: %w", err)
	}

	// Fetch events
	selectQuery := fmt.Sprintf(`
		SELECT id, timestamp, severity, category, message, source, metadata
		FROM events %s
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, query.Limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.Event, 0, query.Limit)
	for rows.Next() {
		var event domain.Event
		var metadataStr sql.NullString
		if err := rows.Scan(&event.ID, &event.Timestamp, &event.Severity, &event.Category, &event.Message, &event.Source, &metadataStr); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if metadataStr.Valid && metadataStr.String != "" {
			event.Metadata = json.RawMessage(metadataStr.String)
		}
		events = append(events, event)
	}

	return &domain.EventQueryResult{
		Events:  events,
		Total:   total,
		HasMore: query.Offset+len(events) < total,
	}, nil
}

// GetRecent returns the most recent N events.
func (s *EventService) GetRecent(n int) []domain.Event {
	if n <= 0 {
		n = 50
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	count := n
	if count > s.count {
		count = s.count
	}

	result := make([]domain.Event, 0, count)
	for i := 0; i < count; i++ {
		idx := (s.head - 1 - i + s.cfg.RingBufferSize) % s.cfg.RingBufferSize
		event := s.events[idx]
		if event.ID == "" {
			continue
		}
		result = append(result, event)
	}

	return result
}

// matchesFilter checks if an event matches the filter criteria.
func (s *EventService) matchesFilter(event domain.Event, filter domain.EventFilter) bool {
	if filter.Severity != nil && event.Severity != *filter.Severity {
		return false
	}
	if filter.Category != nil && event.Category != *filter.Category {
		return false
	}
	if filter.Source != "" && event.Source != filter.Source {
		return false
	}
	if filter.StartTime != nil && event.Timestamp.Before(*filter.StartTime) {
		return false
	}
	if filter.EndTime != nil && event.Timestamp.After(*filter.EndTime) {
		return false
	}
	if filter.SearchText != "" && !strings.Contains(strings.ToLower(event.Message), strings.ToLower(filter.SearchText)) {
		return false
	}
	return true
}

// Subscribe creates a new SSE subscriber and returns a channel for events.
// The caller must call Unsubscribe when done.
func (s *EventService) Subscribe() (uint64, <-chan domain.Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	s.subSeq++
	id := s.subSeq
	ch := make(chan domain.Event, 100) // Buffer to prevent blocking
	s.subscribers[id] = ch

	s.logger.Info("SSE subscriber added", "subscriber_id", id, "total_subscribers", len(s.subscribers))
	return id, ch
}

// Unsubscribe removes an SSE subscriber.
func (s *EventService) Unsubscribe(id uint64) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	if ch, ok := s.subscribers[id]; ok {
		close(ch)
		delete(s.subscribers, id)
		s.logger.Info("SSE subscriber removed", "subscriber_id", id, "total_subscribers", len(s.subscribers))
	}
}

// notifySubscribers sends an event to all SSE subscribers.
func (s *EventService) notifySubscribers(event domain.Event) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()

	for id, ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// Channel full, skip this event for this subscriber
			s.logger.Warn("SSE subscriber buffer full, dropping event", "subscriber_id", id, "event_id", event.ID)
		}
	}
}

// SubscriberCount returns the number of active SSE subscribers.
func (s *EventService) SubscriberCount() int {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	return len(s.subscribers)
}

// Stats returns statistics about the event service.
type EventStats struct {
	BufferSize      int `json:"buffer_size"`
	BufferUsed      int `json:"buffer_used"`
	SSESubscribers  int `json:"sse_subscribers"`
	SQLiteEnabled   bool `json:"sqlite_enabled"`
}

func (s *EventService) Stats() EventStats {
	s.mu.RLock()
	bufferUsed := s.count
	s.mu.RUnlock()

	return EventStats{
		BufferSize:     s.cfg.RingBufferSize,
		BufferUsed:     bufferUsed,
		SSESubscribers: s.SubscriberCount(),
		SQLiteEnabled:  s.db != nil,
	}
}

// CleanupOldEvents removes events older than retention period from SQLite.
func (s *EventService) CleanupOldEvents(ctx context.Context) error {
	if s.db == nil || s.cfg.RetentionDays <= 0 {
		return nil
	}

	cutoff := time.Now().AddDate(0, 0, -s.cfg.RetentionDays)
	result, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE timestamp < ?", cutoff)
	if err != nil {
		return fmt.Errorf("delete old events: %w", err)
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		s.logger.Info("cleaned up old events", "deleted", deleted, "cutoff", cutoff)
	}

	return nil
}
