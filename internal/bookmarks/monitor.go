package bookmarks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/service"
	"github.com/iconidentify/xgrabba/pkg/twitter"
)

type bookmarkLister interface {
	ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) ([]string, string, error)
}

type archiver interface {
	Archive(ctx context.Context, req service.ArchiveRequest) (*service.ArchiveResponse, error)
}

// Monitor polls X bookmarks and triggers archiving for new bookmark IDs.
type Monitor struct {
	cfg    config.BookmarksConfig
	client bookmarkLister
	arch   archiver
	logger *slog.Logger

	seen map[string]time.Time
}

func NewMonitor(cfg config.BookmarksConfig, client bookmarkLister, tweetSvc archiver, logger *slog.Logger) *Monitor {
	return &Monitor{
		cfg:    cfg,
		client: client,
		arch:   tweetSvc,
		logger: logger,
		seen:   make(map[string]time.Time),
	}
}

func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		return
	}
	m.logger.Info("starting bookmarks monitor",
		"user_id", m.cfg.UserID,
		"poll_interval", m.cfg.PollInterval.String(),
		"max_results", m.cfg.MaxResults,
		"max_new_per_poll", m.cfg.MaxNewPerPoll,
	)

	// Run immediately, then on interval.
	m.pollOnce(ctx)

	t := time.NewTicker(m.cfg.PollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("bookmarks monitor stopped")
			return
		case <-t.C:
			m.pollOnce(ctx)
		}
	}
}

func (m *Monitor) pollOnce(ctx context.Context) {
	if m.cfg.UserID == "" {
		m.logger.Warn("bookmarks monitor missing user id; skipping")
		return
	}

	ids, _, err := m.client.ListBookmarks(ctx, m.cfg.UserID, m.cfg.MaxResults, "")
	if err != nil {
		var rl *twitter.RateLimitError
		if errors.As(err, &rl) {
			sleepFor := 30 * time.Second
			if !rl.Reset.IsZero() {
				until := time.Until(rl.Reset.Add(2 * time.Second))
				if until > 0 {
					sleepFor = until
				}
			}
			m.logger.Warn("bookmarks rate limited; backing off", "sleep", sleepFor.String())
			timer := time.NewTimer(sleepFor)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			return
		}

		m.logger.Warn("bookmarks poll failed", "error", err)
		return
	}

	now := time.Now()
	m.pruneSeen(now)

	newIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := m.seen[id]; ok {
			continue
		}
		newIDs = append(newIDs, id)
		if len(newIDs) >= m.cfg.MaxNewPerPoll {
			break
		}
	}

	// Mark all returned IDs as seen so we don't treat them as new on next poll.
	for _, id := range ids {
		if id != "" {
			m.seen[id] = now
		}
	}

	if len(newIDs) == 0 {
		return
	}

	m.logger.Info("new bookmarks detected", "count", len(newIDs))
	for _, id := range newIDs {
		// Use placeholder username - the syndication API doesn't require the real username
		tweetURL := fmt.Sprintf("https://x.com/x/status/%s", id)
		_, err := m.arch.Archive(ctx, service.ArchiveRequest{TweetURL: tweetURL})
		if err != nil {
			m.logger.Warn("failed to enqueue bookmark archive", "tweet_id", id, "error", err)
			continue
		}
		m.logger.Info("bookmark enqueued for archiving", "tweet_id", id)
	}
}

func (m *Monitor) pruneSeen(now time.Time) {
	if m.cfg.SeenTTL <= 0 {
		return
	}
	cutoff := now.Add(-m.cfg.SeenTTL)
	for id, ts := range m.seen {
		if ts.Before(cutoff) {
			delete(m.seen, id)
		}
	}
}
