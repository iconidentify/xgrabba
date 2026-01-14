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
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
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

// MonitorState represents the current state of the bookmark monitor.
type MonitorState string

const (
	MonitorStateIdle    MonitorState = "idle"
	MonitorStateRunning MonitorState = "running"
	MonitorStatePaused  MonitorState = "paused"
)

// Monitor polls X bookmarks and triggers archiving for new bookmark IDs.
type Monitor struct {
	cfg          config.BookmarksConfig
	client       bookmarkLister
	arch         archiver
	logger       *slog.Logger
	eventEmitter domain.EventEmitter

	seen          map[string]time.Time
	rateLimitFile string

	// State management for pause/resume
	mu        sync.RWMutex
	state     MonitorState
	checkNow  chan struct{}
	activity  *ActivityLog
	lastPoll  time.Time
	lastError string
}

func NewMonitor(cfg config.BookmarksConfig, client bookmarkLister, tweetSvc archiver, logger *slog.Logger) *Monitor {
	// Store rate limit state next to the OAuth file
	rateLimitFile := ""
	activityPath := ""
	if cfg.OAuthStorePath != "" {
		dir := filepath.Dir(cfg.OAuthStorePath)
		rateLimitFile = filepath.Join(dir, ".x_bookmarks_ratelimit.json")
		activityPath = filepath.Join(dir, ".x_bookmarks_activity.jsonl")
	}

	return &Monitor{
		cfg:           cfg,
		client:        client,
		arch:          tweetSvc,
		logger:        logger,
		seen:          make(map[string]time.Time),
		rateLimitFile: rateLimitFile,
		state:         MonitorStateIdle,
		checkNow:      make(chan struct{}, 1),
		activity:      NewActivityLog(activityPath, 100),
	}
}

// SetEventEmitter sets the event emitter for the monitor.
func (m *Monitor) SetEventEmitter(emitter domain.EventEmitter) {
	m.eventEmitter = emitter
}

// emitEvent emits an event if the event emitter is configured.
func (m *Monitor) emitEvent(severity domain.EventSeverity, message string, metadata domain.EventMetadata) {
	if m.eventEmitter == nil {
		return
	}
	m.eventEmitter.Emit(domain.Event{
		Timestamp: time.Now(),
		Severity:  severity,
		Category:  domain.EventCategoryBookmarks,
		Message:   message,
		Source:    "BookmarksMonitor",
		Metadata:  metadata.ToJSON(),
	})
}

// State returns the current monitor state.
func (m *Monitor) State() MonitorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// LastPoll returns the timestamp of the last poll attempt.
func (m *Monitor) LastPoll() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastPoll
}

// LastError returns the last error message, if any.
func (m *Monitor) LastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// Pause pauses the monitor polling.
func (m *Monitor) Pause() {
	m.mu.Lock()
	if m.state == MonitorStateRunning {
		m.state = MonitorStatePaused
		m.logger.Info("bookmarks monitor paused")
		_ = m.activity.Append(ActivityEvent{Status: "paused"})
	}
	m.mu.Unlock()
}

// Resume resumes the monitor polling.
func (m *Monitor) Resume() {
	m.mu.Lock()
	if m.state == MonitorStatePaused {
		m.state = MonitorStateRunning
		m.logger.Info("bookmarks monitor resumed")
		_ = m.activity.Append(ActivityEvent{Status: "resumed"})
	}
	m.mu.Unlock()
}

// CheckNow triggers an immediate poll (non-blocking).
func (m *Monitor) CheckNow() {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()

	if state != MonitorStateRunning {
		return
	}

	select {
	case m.checkNow <- struct{}{}:
		m.logger.Info("check-now triggered")
		_ = m.activity.Append(ActivityEvent{Status: "check_now"})
	default:
		// Channel full, poll already pending
	}
}

// Activity returns the activity log for external access.
func (m *Monitor) Activity() *ActivityLog {
	return m.activity
}

func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		return
	}

	// Set state to running
	m.mu.Lock()
	m.state = MonitorStateRunning
	m.mu.Unlock()

	// Browser-credentials mode UX: as soon as credentials arrive, trigger an immediate poll.
	// Otherwise the UI can show a stale "credentials not available" error until the next poll interval.
	if m.cfg.UseBrowserCredentials {
		if probe, ok := m.client.(interface{ HasBrowserCredentials() bool }); ok {
			go func() {
				t := time.NewTicker(2 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						if probe.HasBrowserCredentials() {
							m.logger.Info("browser credentials detected; triggering immediate bookmarks poll")
							m.setLastError("")
							m.CheckNow()
							return
						}
					}
				}
			}()
		}
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
			m.mu.Lock()
			m.state = MonitorStateIdle
			m.mu.Unlock()
			m.logger.Info("bookmarks monitor stopped")
			return
		case <-m.checkNow:
			// Immediate poll requested
			m.pollWithRetry(ctx)
		case <-t.C:
			// Skip if paused
			m.mu.RLock()
			paused := m.state == MonitorStatePaused
			m.mu.RUnlock()
			if paused {
				continue
			}
			m.pollWithRetry(ctx)
		}
	}
}

// pollWithRetry attempts to poll and retries only once after rate limit backoff.
// We're conservative here - after a rate limit, we only retry once to avoid
// compounding rate limit issues with X's API.
func (m *Monitor) pollWithRetry(ctx context.Context) {
	success, rateLimited := m.pollOnce(ctx)
	if success || ctx.Err() != nil {
		return
	}

	if rateLimited {
		// pollOnce already waited out the rate limit. Add extra buffer before retry
		// to be respectful of X's API and avoid edge cases with rate limit windows.
		extraBuffer := time.Duration(10+rand.Intn(20)) * time.Second
		m.logger.Info("adding extra buffer before retry", "buffer", extraBuffer.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(extraBuffer):
		}

		// Try ONE more time. If we hit the rate limit again, give up.
		m.logger.Info("retrying bookmark poll after rate limit cleared")
		success, _ = m.pollOnce(ctx)
		if success {
			return
		}
		// Still failing - don't keep retrying, wait for next scheduled poll
		m.logger.Info("bookmark poll still failing after retry, will try again at next poll interval")
	}
}

// pollOnce attempts a single poll. Returns (success, wasRateLimited).
func (m *Monitor) pollOnce(ctx context.Context) (bool, bool) {
	// Update last poll timestamp
	m.mu.Lock()
	m.lastPoll = time.Now()
	m.mu.Unlock()

	// UserID is only required for API v2 bookmarks; GraphQL browser-session mode is user-bound.
	if !m.cfg.UseBrowserCredentials && m.cfg.UserID == "" {
		m.logger.Warn("bookmarks monitor missing user id; skipping")
		m.setLastError("missing user id")
		return false, false
	}

	ids, _, err := m.client.ListBookmarks(ctx, m.cfg.UserID, m.cfg.MaxResults, "")
	if err != nil {
		var rl *twitter.RateLimitError
		if errors.As(err, &rl) {
			sleepFor := 30 * time.Second
			resetTime := time.Now().Add(sleepFor)
			if !rl.Reset.IsZero() {
				until := time.Until(rl.Reset.Add(2 * time.Second))
				if until > 0 {
					sleepFor = until
				}
				resetTime = rl.Reset.Add(2 * time.Second)
				// Persist the rate limit so we respect it across restarts
				m.saveRateLimitState(resetTime)
			} else {
				// No reset time provided, use exponential backoff style wait
				// and persist a conservative estimate
				m.saveRateLimitState(resetTime)
			}

			m.logger.Warn("bookmarks rate limited; backing off", "sleep", sleepFor.Round(time.Second).String())
			m.setLastError("rate limited")
			_ = m.activity.Append(ActivityEvent{
				Status:         "rate_limited",
				Error:          "rate limited by X API",
				RateLimitReset: &resetTime,
			})

			// Emit rate limit event
			m.emitEvent(domain.EventSeverityWarning,
				fmt.Sprintf("X API rate limit hit, waiting %s", sleepFor.Round(time.Second)),
				domain.EventMetadata{"reset_at": resetTime.Format(time.RFC3339), "wait_duration": sleepFor.String()})

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
		m.setLastError(err.Error())
		_ = m.activity.Append(ActivityEvent{
			Status: "failed",
			Error:  err.Error(),
		})
		return false, false
	}

	// Clear last error on success
	m.setLastError("")

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
		_ = m.activity.Append(ActivityEvent{
			Status:         "success",
			TotalBookmarks: len(ids),
			NewBookmarks:   0,
		})
		return true, false
	}

	m.logger.Info("new bookmarks detected", "count", len(newIDs))

	archivedIDs := make([]string, 0, len(newIDs))
	for _, id := range newIDs {
		// Use placeholder username - the syndication API doesn't require the real username
		tweetURL := fmt.Sprintf("https://x.com/x/status/%s", id)
		_, err := m.arch.Archive(ctx, service.ArchiveRequest{TweetURL: tweetURL})
		if err != nil {
			m.logger.Warn("failed to enqueue bookmark archive", "tweet_id", id, "error", err)
			continue
		}
		archivedIDs = append(archivedIDs, id)
		m.logger.Info("bookmark enqueued for archiving", "tweet_id", id)
	}

	// Log success with new bookmarks
	_ = m.activity.Append(ActivityEvent{
		Status:         "success",
		TotalBookmarks: len(ids),
		NewBookmarks:   len(newIDs),
		ArchivedIDs:    archivedIDs,
	})

	// Emit event for new bookmarks found
	if len(archivedIDs) > 0 {
		m.emitEvent(domain.EventSeveritySuccess,
			fmt.Sprintf("Found %d new bookmarks, queued for archiving", len(archivedIDs)),
			domain.EventMetadata{"new_count": len(archivedIDs), "total_count": len(ids), "tweet_ids": archivedIDs})
	}

	return true, false
}

// setLastError updates the last error message (thread-safe).
func (m *Monitor) setLastError(err string) {
	m.mu.Lock()
	m.lastError = err
	m.mu.Unlock()
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
