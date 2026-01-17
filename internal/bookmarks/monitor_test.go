package bookmarks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/service"
)

type fakeClient struct {
	ids []string
}

func (f *fakeClient) ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) ([]string, string, error) {
	return f.ids, "", nil
}

type fakeArchiver struct {
	mu   sync.Mutex
	urls []string
}

func (f *fakeArchiver) Archive(ctx context.Context, req service.ArchiveRequest) (*service.ArchiveResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, req.TweetURL)
	return &service.ArchiveResponse{TweetID: "x"}, nil
}

func TestMonitor_NewBookmarksTriggerArchive(t *testing.T) {
	fc := &fakeClient{ids: []string{"3", "2", "1"}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:       true,
		UserID:        "u",
		PollInterval:  1 * time.Hour,
		MaxResults:    100,
		MaxNewPerPoll: 10,
		SeenTTL:       24 * time.Hour,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.pollOnce(context.Background())

	fa.mu.Lock()
	defer fa.mu.Unlock()
	if len(fa.urls) != 3 {
		t.Fatalf("expected 3 archives, got %d: %#v", len(fa.urls), fa.urls)
	}
}

func TestMonitor_StateManagement(t *testing.T) {
	fc := &fakeClient{ids: []string{}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Initial state should be idle
	if m.State() != MonitorStateIdle {
		t.Errorf("initial state = %q, want idle", m.State())
	}

	// Start monitor to set it to running
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.Start(ctx)
	time.Sleep(100 * time.Millisecond) // Give it time to start and set state to running

	// Pause should work when running
	m.Pause()
	time.Sleep(10 * time.Millisecond)
	if m.State() != MonitorStatePaused {
		t.Errorf("state after pause = %q, want paused", m.State())
	}

	m.Resume()
	time.Sleep(10 * time.Millisecond)
	if m.State() != MonitorStateRunning {
		t.Errorf("state after resume = %q, want running", m.State())
	}
}

func TestMonitor_CheckNow(t *testing.T) {
	fc := &fakeClient{ids: []string{}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Start monitor to set state to running
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.Start(ctx)
	time.Sleep(50 * time.Millisecond) // Give it time to start

	// CheckNow should work when running
	m.CheckNow()

	// CheckNow should not work when paused
	m.Pause()
	m.CheckNow() // Should be no-op
}

func TestMonitor_FailedCache(t *testing.T) {
	fc := &fakeClient{ids: []string{}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Initially empty
	snapshot := m.FailedCache()
	if snapshot.Count != 0 {
		t.Errorf("initial failed cache count = %d, want 0", snapshot.Count)
	}

	// Clear should work
	m.ClearFailedCache()
	snapshot = m.FailedCache()
	if snapshot.Count != 0 {
		t.Errorf("failed cache count after clear = %d, want 0", snapshot.Count)
	}
}

func TestMonitor_LastPollAndError(t *testing.T) {
	fc := &fakeClient{ids: []string{}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Initially zero time
	lastPoll := m.LastPoll()
	if !lastPoll.IsZero() {
		t.Errorf("initial LastPoll = %v, want zero time", lastPoll)
	}

	// Initially empty error
	lastError := m.LastError()
	if lastError != "" {
		t.Errorf("initial LastError = %q, want empty", lastError)
	}
}

func TestMonitor_ActivityLog(t *testing.T) {
	fc := &fakeClient{ids: []string{}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	activity := m.Activity()
	if activity == nil {
		t.Fatal("Activity should not be nil")
	}
}

func TestMonitor_SeenBookmarksNotArchived(t *testing.T) {
	fc := &fakeClient{ids: []string{"1", "2", "3"}}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
		MaxNewPerPoll: 10,
		SeenTTL:       24 * time.Hour,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// First poll - should archive all
	m.pollOnce(context.Background())

	fa.mu.Lock()
	firstCount := len(fa.urls)
	fa.mu.Unlock()

	if firstCount != 3 {
		t.Fatalf("first poll: expected 3 archives, got %d", firstCount)
	}

	// Second poll with same IDs - should not archive again
	m.pollOnce(context.Background())

	fa.mu.Lock()
	secondCount := len(fa.urls)
	fa.mu.Unlock()

	if secondCount != 3 {
		t.Errorf("second poll: expected 3 total archives (no new), got %d", secondCount)
	}
}

type errorClient struct{}

func (e *errorClient) ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) ([]string, string, error) {
	return nil, "", fmt.Errorf("API error")
}

func TestMonitor_HandlesClientError(t *testing.T) {
	fc := &errorClient{}
	fa := &fakeArchiver{}

	cfg := config.BookmarksConfig{
		Enabled:      true,
		UserID:        "u",
		PollInterval: 1 * time.Hour,
		MaxResults:    100,
	}

	m := NewMonitor(cfg, fc, fa, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Should handle error gracefully
	m.pollOnce(context.Background())

	// Should have error recorded
	lastError := m.LastError()
	if lastError == "" {
		t.Error("expected error to be recorded")
	}
}

