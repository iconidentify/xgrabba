package bookmarks

import (
	"context"
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

