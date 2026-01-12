package bookmarks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
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

// rateLimitState persists rate limit info across restarts.
type rateLimitState struct {
	ResetAt time.Time `json:"reset_at"`
}

// Monitor polls X bookmarks and triggers archiving for new bookmark IDs.
type Monitor struct {
	cfg    config.BookmarksConfig
	client bookmarkLister
	arch   archiver
	logger *slog.Logger

	seen          map[string]time.Time
	rateLimitFile string
}

func NewMonitor(cfg config.BookmarksConfig, client bookmarkLister, tweetSvc archiver, logger *slog.Logger) *Monitor {
	// Store rate limit state next to the OAuth file
	rateLimitFile := ""
	if cfg.OAuthStorePath != "" {
		rateLimitFile = filepath.Join(filepath.Dir(cfg.OAuthStorePath), ".x_bookmarks_ratelimit.json")
	}

	return &Monitor{
		cfg:           cfg,
		client:        client,
		arch:          tweetSvc,
		logger:        logger,
		seen:          make(map[string]time.Time),
		rateLimitFile: rateLimitFile,
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

	// Check for persisted rate limit state from previous crash/restart
	if resetAt := m.loadRateLimitState(); !resetAt.IsZero() {
		if wait := time.Until(resetAt); wait > 0 {
			m.logger.Info("respecting persisted rate limit from previous run", "wait", wait.Round(time.Second).String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			m.clearRateLimitState()
		}
	}

	// Add startup jitter (5-15 seconds) to avoid thundering herd on crash loops
	jitter := time.Duration(5+rand.Intn(10)) * time.Second
	m.logger.Info("startup delay before first poll", "delay", jitter.String())
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	// Run first poll, then on interval
	m.pollWithRetry(ctx)

	t := time.NewTicker(m.cfg.PollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("bookmarks monitor stopped")
			return
		case <-t.C:
			m.pollWithRetry(ctx)
		}
	}
}

// pollWithRetry attempts to poll and retries after rate limit backoff.
func (m *Monitor) pollWithRetry(ctx context.Context) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			m.logger.Info("retrying bookmark poll", "attempt", attempt+1)
		}

		success, rateLimited := m.pollOnce(ctx)
		if success {
			return
		}
		if !rateLimited {
			// Non-rate-limit error, don't retry immediately
			return
		}
		// Rate limited - pollOnce already waited, try again
		if ctx.Err() != nil {
			return
		}
	}
	m.logger.Warn("bookmark poll failed after max retries", "retries", maxRetries)
}

// pollOnce attempts a single poll. Returns (success, wasRateLimited).
func (m *Monitor) pollOnce(ctx context.Context) (bool, bool) {
	if m.cfg.UserID == "" {
		m.logger.Warn("bookmarks monitor missing user id; skipping")
		return false, false
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
				// Persist the rate limit so we respect it across restarts
				m.saveRateLimitState(rl.Reset.Add(2 * time.Second))
			} else {
				// No reset time provided, use exponential backoff style wait
				// and persist a conservative estimate
				m.saveRateLimitState(time.Now().Add(sleepFor))
			}

			m.logger.Warn("bookmarks rate limited; backing off", "sleep", sleepFor.Round(time.Second).String())
			timer := time.NewTimer(sleepFor)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false, true
			case <-timer.C:
			}
			m.clearRateLimitState()
			return false, true // Was rate limited, caller can retry
		}

		m.logger.Warn("bookmarks poll failed", "error", err)
		return false, false
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
		m.logger.Info("bookmark poll complete", "total_bookmarks", len(ids), "new", 0)
		return true, false
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
	return true, false
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

func (m *Monitor) loadRateLimitState() time.Time {
	if m.rateLimitFile == "" {
		return time.Time{}
	}
	data, err := os.ReadFile(m.rateLimitFile)
	if err != nil {
		return time.Time{}
	}
	var state rateLimitState
	if err := json.Unmarshal(data, &state); err != nil {
		return time.Time{}
	}
	return state.ResetAt
}

func (m *Monitor) saveRateLimitState(resetAt time.Time) {
	if m.rateLimitFile == "" {
		return
	}
	state := rateLimitState{ResetAt: resetAt}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(m.rateLimitFile, data, 0600)
}

func (m *Monitor) clearRateLimitState() {
	if m.rateLimitFile == "" {
		return
	}
	_ = os.Remove(m.rateLimitFile)
}
