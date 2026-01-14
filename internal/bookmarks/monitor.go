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

// failedTweetsCache persists tweet IDs that permanently failed archiving.
// This prevents re-queueing tweets from suspended/deleted accounts every poll.
type failedTweetsCache struct {
	// FailedIDs maps tweet ID to failure reason and timestamp
	FailedIDs map[string]failedTweetEntry `json:"failed_ids"`
}

type failedTweetEntry struct {
	Reason    string    `json:"reason"`
	FailedAt  time.Time `json:"failed_at"`
	Attempts  int       `json:"attempts"`
}

// permanentFailureReasons are error substrings that indicate a tweet will never succeed.
var permanentFailureReasons = []string{
	"author data unavailable",
	"account suspended",
	"account is suspended",
	"tweet not found",
	"tweet has been deleted",
	"protected tweets",
	"User not found",
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

	seen            map[string]time.Time
	rateLimitFile   string
	failedCacheFile string
	failedCache     *failedTweetsCache

	// State management for pause/resume
	mu        sync.RWMutex
	state     MonitorState
	checkNow  chan struct{}
	activity  *ActivityLog
	lastPoll  time.Time
	lastError string
}

// FailedCacheSnapshot is a stable view of the permanent-failure cache.
// It is used to avoid re-queueing the same permanently failing tweet IDs every poll.
type FailedCacheSnapshot struct {
	Count     int                         `json:"count"`
	FailedIDs map[string]failedTweetEntry `json:"failed_ids"`
}

func NewMonitor(cfg config.BookmarksConfig, client bookmarkLister, tweetSvc archiver, logger *slog.Logger) *Monitor {
	// Store rate limit state next to the OAuth file
	rateLimitFile := ""
	failedCacheFile := ""
	activityPath := ""
	if cfg.OAuthStorePath != "" {
		dir := filepath.Dir(cfg.OAuthStorePath)
		rateLimitFile = filepath.Join(dir, ".x_bookmarks_ratelimit.json")
		failedCacheFile = filepath.Join(dir, ".x_bookmarks_failed.json")
		activityPath = filepath.Join(dir, ".x_bookmarks_activity.jsonl")
	}

	m := &Monitor{
		cfg:             cfg,
		client:          client,
		arch:            tweetSvc,
		logger:          logger,
		seen:            make(map[string]time.Time),
		rateLimitFile:   rateLimitFile,
		failedCacheFile: failedCacheFile,
		failedCache:     &failedTweetsCache{FailedIDs: make(map[string]failedTweetEntry)},
		state:           MonitorStateIdle,
		checkNow:        make(chan struct{}, 1),
		activity:        NewActivityLog(activityPath, 100),
	}

	// Load any persisted failed tweets cache
	m.loadFailedCache()

	return m
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

// FailedCache returns a snapshot of the permanent failure cache.
func (m *Monitor) FailedCache() FailedCacheSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := FailedCacheSnapshot{
		FailedIDs: map[string]failedTweetEntry{},
	}
	if m.failedCache == nil || m.failedCache.FailedIDs == nil {
		return out
	}
	for k, v := range m.failedCache.FailedIDs {
		out.FailedIDs[k] = v
	}
	out.Count = len(out.FailedIDs)
	return out
}

// ClearFailedCache clears the permanent failure cache (in-memory and on-disk).
func (m *Monitor) ClearFailedCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failedCache = &failedTweetsCache{FailedIDs: make(map[string]failedTweetEntry)}
	if m.failedCacheFile != "" {
		_ = os.Remove(m.failedCacheFile)
	}
	m.logger.Info("cleared failed tweets cache")
	_ = m.activity.Append(ActivityEvent{Status: "failed_cache_cleared"})
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
	skippedFailed := 0
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := m.seen[id]; ok {
			continue
		}
		// Skip tweets that permanently failed (suspended/deleted accounts)
		if m.isFailedTweet(id) {
			skippedFailed++
			continue
		}
		newIDs = append(newIDs, id)
		if len(newIDs) >= m.cfg.MaxNewPerPoll {
			break
		}
	}

	if skippedFailed > 0 {
		m.logger.Debug("skipped permanently failed tweets", "count", skippedFailed)
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
		resp, err := m.arch.Archive(ctx, service.ArchiveRequest{TweetURL: tweetURL})
		if err != nil {
			m.logger.Warn("failed to enqueue bookmark archive", "tweet_id", id, "error", err)
			// Mark as permanently failed if it's an unrecoverable error
			if isPermanentFailure(err) {
				m.markTweetFailed(id, err.Error())
				m.logger.Info("marked tweet as permanently failed", "tweet_id", id, "reason", err.Error())
			}
			continue
		}
		// Check if the tweet service returned a permanently failed status
		if resp != nil && resp.Status == domain.ArchiveStatusFailed {
			m.markTweetFailed(id, resp.Message)
			m.logger.Info("tweet permanently unavailable", "tweet_id", id, "reason", resp.Message)
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

// loadFailedCache loads the persisted failed tweets cache from disk.
func (m *Monitor) loadFailedCache() {
	if m.failedCacheFile == "" {
		return
	}
	data, err := os.ReadFile(m.failedCacheFile)
	if err != nil {
		return
	}
	var cache failedTweetsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		m.logger.Warn("failed to parse failed tweets cache", "error", err)
		return
	}
	if cache.FailedIDs != nil {
		m.failedCache = &cache
		m.logger.Info("loaded failed tweets cache", "count", len(cache.FailedIDs))
	}
}

// saveFailedCache persists the failed tweets cache to disk.
func (m *Monitor) saveFailedCache() {
	if m.failedCacheFile == "" {
		return
	}
	data, err := json.Marshal(m.failedCache)
	if err != nil {
		return
	}
	_ = os.WriteFile(m.failedCacheFile, data, 0600)
}

// isFailedTweet checks if a tweet ID is in the permanent failure cache.
func (m *Monitor) isFailedTweet(id string) bool {
	if m.failedCache == nil {
		return false
	}
	_, exists := m.failedCache.FailedIDs[id]
	return exists
}

// markTweetFailed adds a tweet ID to the permanent failure cache.
func (m *Monitor) markTweetFailed(id string, reason string) {
	if m.failedCache == nil {
		m.failedCache = &failedTweetsCache{FailedIDs: make(map[string]failedTweetEntry)}
	}

	entry, exists := m.failedCache.FailedIDs[id]
	if exists {
		entry.Attempts++
		entry.Reason = reason
		m.failedCache.FailedIDs[id] = entry
	} else {
		m.failedCache.FailedIDs[id] = failedTweetEntry{
			Reason:   reason,
			FailedAt: time.Now(),
			Attempts: 1,
		}
	}
	m.saveFailedCache()
}

// isPermanentFailure checks if an error indicates a permanent failure.
func isPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	for _, reason := range permanentFailureReasons {
		if contains(errStr, reason) {
			return true
		}
	}
	return false
}

// contains checks if s contains substr (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsLower(s, substr)))
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if matchesAt(s, i, substr) {
			return true
		}
	}
	return false
}

func matchesAt(s string, pos int, substr string) bool {
	for j := 0; j < len(substr); j++ {
		sc := s[pos+j]
		pc := substr[j]
		// Simple lowercase for ASCII
		if sc >= 'A' && sc <= 'Z' {
			sc += 'a' - 'A'
		}
		if pc >= 'A' && pc <= 'Z' {
			pc += 'a' - 'A'
		}
		if sc != pc {
			return false
		}
	}
	return true
}
