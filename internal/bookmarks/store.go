package bookmarks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type OAuthStore struct {
	UserID       string    `json:"user_id"`
	RefreshToken string    `json:"refresh_token"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ActivityEvent represents a single bookmark poll event for the activity log.
type ActivityEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	Status         string    `json:"status"` // "success", "rate_limited", "failed", "paused", "resumed", "check_now"
	TotalBookmarks int       `json:"total_bookmarks,omitempty"`
	NewBookmarks   int       `json:"new_bookmarks,omitempty"`
	ArchivedIDs    []string  `json:"archived_ids,omitempty"`
	Error          string    `json:"error,omitempty"`
	RateLimitReset *time.Time `json:"rate_limit_reset,omitempty"`
}

// ActivityLog manages the activity log file (JSON lines format).
type ActivityLog struct {
	path string
	mu   sync.Mutex
	max  int // max entries to keep
}

// NewActivityLog creates a new activity log manager.
func NewActivityLog(path string, maxEntries int) *ActivityLog {
	if maxEntries <= 0 {
		maxEntries = 100
	}
	return &ActivityLog{
		path: path,
		max:  maxEntries,
	}
}

// Append adds an event to the activity log.
func (a *ActivityLog) Append(event ActivityEvent) error {
	if a.path == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(a.path), 0755); err != nil {
		return fmt.Errorf("create activity log dir: %w", err)
	}

	// Read existing entries
	entries, _ := a.readEntriesLocked()

	// Add new event
	event.Timestamp = time.Now()
	entries = append(entries, event)

	// Trim to max
	if len(entries) > a.max {
		entries = entries[len(entries)-a.max:]
	}

	// Write all entries back
	return a.writeEntriesLocked(entries)
}

// GetRecent returns the most recent events (newest first).
func (a *ActivityLog) GetRecent(limit int) ([]ActivityEvent, error) {
	if a.path == "" {
		return nil, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	entries, err := a.readEntriesLocked()
	if err != nil {
		return nil, err
	}

	// Reverse to newest first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	return entries, nil
}

func (a *ActivityLog) readEntriesLocked() ([]ActivityEvent, error) {
	f, err := os.Open(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []ActivityEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event ActivityEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, event)
	}
	return entries, scanner.Err()
}

func (a *ActivityLog) writeEntriesLocked(entries []ActivityEvent) error {
	f, err := os.Create(a.path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		f.Write(data)
		f.WriteString("\n")
	}
	return nil
}

func LoadOAuthStore(path string) (*OAuthStore, error) {
	if path == "" {
		return nil, fmt.Errorf("store path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s OAuthStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode oauth store: %w", err)
	}
	return &s, nil
}

func SaveOAuthStore(path string, store OAuthStore) error {
	if path == "" {
		return fmt.Errorf("store path is empty")
	}
	store.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode oauth store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	// 0600: keep secrets private on disk
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write oauth store: %w", err)
	}
	return nil
}

func DeleteOAuthStore(path string) error {
	if path == "" {
		return fmt.Errorf("store path is empty")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

