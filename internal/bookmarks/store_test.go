package bookmarks

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOAuthStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "store.json")

	if err := SaveOAuthStore(p, OAuthStore{UserID: "123", RefreshToken: "rt"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := LoadOAuthStore(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if st.UserID != "123" || st.RefreshToken != "rt" {
		t.Fatalf("unexpected store: %#v", st)
	}

	if err := DeleteOAuthStore(p); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("expected deleted, got err=%v", err)
	}
}

func TestOAuthStore_LoadNotExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nonexistent.json")

	_, err := LoadOAuthStore(p)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestOAuthStore_LoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "invalid.json")
	os.WriteFile(p, []byte("invalid json{"), 0644)

	_, err := LoadOAuthStore(p)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestOAuthStore_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "subdir", "store.json")

	if err := SaveOAuthStore(p, OAuthStore{UserID: "test"}); err != nil {
		t.Fatalf("save should create directory: %v", err)
	}

	if _, err := os.Stat(p); os.IsNotExist(err) {
		t.Error("file should be created")
	}
}

func TestActivityLog_Append(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "activity.jsonl")
	log := NewActivityLog(logPath, 10)

	event := ActivityEvent{
		Status:         "success",
		TotalBookmarks: 100,
		NewBookmarks:   5,
	}

	if err := log.Append(event); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	recent, err := log.GetRecent(1)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 event, got %d", len(recent))
	}
	if recent[0].Status != "success" {
		t.Errorf("Status = %q, want success", recent[0].Status)
	}
}

func TestActivityLog_AppendEmptyPath(t *testing.T) {
	log := NewActivityLog("", 10)
	event := ActivityEvent{Status: "test"}

	// Should not error with empty path
	if err := log.Append(event); err != nil {
		t.Errorf("Append with empty path should not error: %v", err)
	}
}

func TestActivityLog_GetRecentEmptyPath(t *testing.T) {
	log := NewActivityLog("", 10)

	recent, err := log.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent with empty path should not error: %v", err)
	}
	if len(recent) != 0 {
		t.Errorf("expected empty list, got %d", len(recent))
	}
}

func TestActivityLog_MaxEntries(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "activity.jsonl")
	log := NewActivityLog(logPath, 3) // Max 3 entries

	// Add 5 events
	for i := 0; i < 5; i++ {
		event := ActivityEvent{
			Status:         "success",
			TotalBookmarks: i,
		}
		if err := log.Append(event); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	recent, err := log.GetRecent(10)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 3 {
		t.Errorf("expected 3 entries (max), got %d", len(recent))
	}
}

func TestActivityLog_GetRecentLimit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "activity.jsonl")
	log := NewActivityLog(logPath, 10)

	// Add 5 events
	for i := 0; i < 5; i++ {
		log.Append(ActivityEvent{Status: fmt.Sprintf("event-%d", i)})
	}

	recent, err := log.GetRecent(2)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("expected 2 entries (limit), got %d", len(recent))
	}
}

func TestActivityLog_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "activity.jsonl")
	log := NewActivityLog(logPath, 100)

	// Concurrent appends
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			event := ActivityEvent{
				Status: fmt.Sprintf("event-%d", id),
			}
			_ = log.Append(event)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	recent, err := log.GetRecent(20)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) < 10 {
		t.Errorf("expected at least 10 entries, got %d", len(recent))
	}
}

