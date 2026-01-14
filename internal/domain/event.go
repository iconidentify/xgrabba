package domain

import (
	"encoding/json"
	"time"
)

// EventID is a unique identifier for an event.
type EventID string

// String returns the string representation of the EventID.
func (id EventID) String() string {
	return string(id)
}

// EventSeverity represents the severity level of an event.
type EventSeverity string

const (
	EventSeverityInfo    EventSeverity = "info"
	EventSeverityWarning EventSeverity = "warning"
	EventSeverityError   EventSeverity = "error"
	EventSeveritySuccess EventSeverity = "success"
)

// EventCategory represents the category of an event for filtering.
type EventCategory string

const (
	EventCategoryExport     EventCategory = "export"
	EventCategoryEncryption EventCategory = "encryption"
	EventCategoryUSB        EventCategory = "usb"
	EventCategoryBookmarks  EventCategory = "bookmarks"
	EventCategoryAI         EventCategory = "ai"
	EventCategoryDisk       EventCategory = "disk"
	EventCategoryTweet      EventCategory = "tweet"
	EventCategoryNetwork    EventCategory = "network"
	EventCategorySystem     EventCategory = "system"
)

// Event represents a system event for the activity log.
type Event struct {
	ID        EventID         `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Severity  EventSeverity   `json:"severity"`
	Category  EventCategory   `json:"category"`
	Message   string          `json:"message"`
	Source    string          `json:"source,omitempty"`    // Component that generated the event
	Metadata  json.RawMessage `json:"metadata,omitempty"`  // Optional structured data
}

// EventMetadata is a helper type for building event metadata.
type EventMetadata map[string]interface{}

// ToJSON converts metadata to JSON for storage.
func (m EventMetadata) ToJSON() json.RawMessage {
	if m == nil {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return data
}

// EventFilter specifies criteria for querying events.
type EventFilter struct {
	Severity   *EventSeverity `json:"severity,omitempty"`
	Category   *EventCategory `json:"category,omitempty"`
	Source     string         `json:"source,omitempty"`
	StartTime  *time.Time     `json:"start_time,omitempty"`
	EndTime    *time.Time     `json:"end_time,omitempty"`
	SearchText string         `json:"search_text,omitempty"`
}

// EventEmitter is the interface for components that emit events.
type EventEmitter interface {
	// Emit records an event to the event log.
	Emit(event Event)

	// EmitInfo is a convenience method for info-level events.
	EmitInfo(category EventCategory, source, message string, metadata EventMetadata)

	// EmitWarning is a convenience method for warning-level events.
	EmitWarning(category EventCategory, source, message string, metadata EventMetadata)

	// EmitError is a convenience method for error-level events.
	EmitError(category EventCategory, source, message string, metadata EventMetadata)

	// EmitSuccess is a convenience method for success-level events.
	EmitSuccess(category EventCategory, source, message string, metadata EventMetadata)
}

// EventQuery represents a query for events with pagination.
type EventQuery struct {
	Filter EventFilter `json:"filter"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

// EventQueryResult contains the result of an event query.
type EventQueryResult struct {
	Events  []Event `json:"events"`
	Total   int     `json:"total"`
	HasMore bool    `json:"has_more"`
}
