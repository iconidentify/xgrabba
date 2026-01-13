package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// ExportService handles exporting the archive to portable formats.
type ExportService struct {
	tweetSvc *TweetService
	logger   *slog.Logger

	// Async export state
	mu           sync.Mutex
	activeExport *ActiveExport
}

// ActiveExport tracks an in-progress export operation.
type ActiveExport struct {
	ID             string             `json:"export_id"`
	DestPath       string             `json:"dest_path"`
	Phase          string             `json:"phase"` // preparing, exporting, finalizing, completed, failed, cancelled
	TotalTweets    int                `json:"total_tweets"`
	ExportedTweets int                `json:"exported_tweets"`
	BytesWritten   int64              `json:"bytes_written"`
	CurrentFile    string             `json:"current_file"`
	StartedAt      time.Time          `json:"started_at"`
	Error          string             `json:"error,omitempty"`
	cancelFunc     context.CancelFunc `json:"-"`
}

// ExportEstimate contains size estimates for an export.
type ExportEstimate struct {
	TweetCount         int      `json:"tweet_count"`
	MediaCount         int      `json:"media_count"`
	EstimatedSizeBytes int64    `json:"estimated_size_bytes"`
	Volumes            []Volume `json:"volumes"`
}

// Volume represents an available storage volume.
type Volume struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	FreeBytes int64  `json:"free_bytes"`
}

// NewExportService creates a new export service.
func NewExportService(tweetSvc *TweetService, logger *slog.Logger) *ExportService {
	return &ExportService{
		tweetSvc: tweetSvc,
		logger:   logger,
	}
}

// EstimateExport calculates the estimated size and counts for an export.
func (s *ExportService) EstimateExport(ctx context.Context) (*ExportEstimate, error) {
	tweets, _, err := s.tweetSvc.List(ctx, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("list tweets: %w", err)
	}

	var totalSize int64
	var mediaCount int

	for _, tweet := range tweets {
		// Estimate tweet metadata size (~2KB per tweet)
		totalSize += 2048

		// Add media file sizes
		for _, media := range tweet.Media {
			mediaCount++
			if media.LocalPath != "" {
				if stat, err := os.Stat(media.LocalPath); err == nil {
					totalSize += stat.Size()
				}
			}
		}

		// Avatar estimate (~50KB)
		if tweet.Author.LocalAvatarURL != "" {
			totalSize += 50 * 1024
		}
	}

	// Add overhead for index.html (~350KB) and viewers (~50MB if included)
	totalSize += 350 * 1024

	return &ExportEstimate{
		TweetCount:         len(tweets),
		MediaCount:         mediaCount,
		EstimatedSizeBytes: totalSize,
		Volumes:            s.GetAvailableVolumes(),
	}, nil
}

// GetAvailableVolumes returns a list of available storage volumes (USB drives, etc.).
func (s *ExportService) GetAvailableVolumes() []Volume {
	var volumes []Volume

	switch runtime.GOOS {
	case "darwin":
		// macOS: Check /Volumes
		entries, err := os.ReadDir("/Volumes")
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != "Macintosh HD" {
					path := filepath.Join("/Volumes", entry.Name())
					free := getFreeDiskSpace(path)
					if free > 0 {
						volumes = append(volumes, Volume{
							Path:      path,
							Name:      entry.Name(),
							FreeBytes: free,
						})
					}
				}
			}
		}
	case "linux":
		// Linux: Check /media and /mnt
		for _, base := range []string{"/media", "/mnt"} {
			entries, err := os.ReadDir(base)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						// Check for user subdirectories in /media
						if base == "/media" {
							subpath := filepath.Join(base, entry.Name())
							subentries, err := os.ReadDir(subpath)
							if err == nil {
								for _, subentry := range subentries {
									if subentry.IsDir() {
										path := filepath.Join(subpath, subentry.Name())
										free := getFreeDiskSpace(path)
										if free > 0 {
											volumes = append(volumes, Volume{
												Path:      path,
												Name:      subentry.Name(),
												FreeBytes: free,
											})
										}
									}
								}
							}
						} else {
							path := filepath.Join(base, entry.Name())
							free := getFreeDiskSpace(path)
							if free > 0 {
								volumes = append(volumes, Volume{
									Path:      path,
									Name:      entry.Name(),
									FreeBytes: free,
								})
							}
						}
					}
				}
			}
		}
	case "windows":
		// Windows: Check drive letters D-Z
		for c := 'D'; c <= 'Z'; c++ {
			path := string(c) + ":\\"
			free := getFreeDiskSpace(path)
			if free > 0 {
				volumes = append(volumes, Volume{
					Path:      path,
					Name:      string(c) + ":",
					FreeBytes: free,
				})
			}
		}
	}

	return volumes
}

// ErrExportInProgress is returned when trying to start an export while one is already running.
var ErrExportInProgress = fmt.Errorf("export already in progress")

// StartExportAsync starts an export operation in the background.
func (s *ExportService) StartExportAsync(opts ExportOptions) (string, error) {
	s.mu.Lock()
	if s.activeExport != nil && (s.activeExport.Phase == "preparing" || s.activeExport.Phase == "exporting" || s.activeExport.Phase == "finalizing") {
		s.mu.Unlock()
		return "", ErrExportInProgress
	}

	// Generate export ID
	exportID := fmt.Sprintf("exp_%d", time.Now().UnixNano())

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	s.activeExport = &ActiveExport{
		ID:         exportID,
		DestPath:   opts.DestPath,
		Phase:      "preparing",
		StartedAt:  time.Now(),
		cancelFunc: cancel,
	}
	s.mu.Unlock()

	// Start export in background
	go s.runExportAsync(ctx, opts)

	return exportID, nil
}

// runExportAsync runs the export operation and updates progress.
func (s *ExportService) runExportAsync(ctx context.Context, opts ExportOptions) {
	defer func() {
		// Ensure phase is set on exit if not already completed/failed/cancelled
		s.mu.Lock()
		if s.activeExport != nil && s.activeExport.Phase != "completed" && s.activeExport.Phase != "failed" && s.activeExport.Phase != "cancelled" {
			s.activeExport.Phase = "failed"
			s.activeExport.Error = "unexpected exit"
		}
		s.mu.Unlock()
	}()

	// Validate destination
	if opts.DestPath == "" {
		s.setExportError("destination path is required")
		return
	}

	// Create destination directory
	if err := os.MkdirAll(opts.DestPath, 0755); err != nil {
		s.setExportError(fmt.Sprintf("create destination directory: %v", err))
		return
	}

	// Get all tweets
	tweets, _, err := s.tweetSvc.List(ctx, 0, 0)
	if err != nil {
		s.setExportError(fmt.Sprintf("list tweets: %v", err))
		return
	}

	// Apply filters
	if opts.DateRange != nil || len(opts.Authors) > 0 || opts.SearchQuery != "" {
		tweets = s.filterTweets(tweets, opts)
	}

	// Sort by date (newest first)
	sort.Slice(tweets, func(i, j int) bool {
		return tweets[i].CreatedAt.After(tweets[j].CreatedAt)
	})

	// Update total count
	s.mu.Lock()
	s.activeExport.TotalTweets = len(tweets)
	s.activeExport.Phase = "exporting"
	s.mu.Unlock()

	// Create data directory
	dataDir := filepath.Join(opts.DestPath, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		s.setExportError(fmt.Sprintf("create data directory: %v", err))
		return
	}

	// Export tweets and media
	exportedTweets := make([]ExportedTweet, 0, len(tweets))

	for i, tweet := range tweets {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.activeExport.Phase = "cancelled"
			s.mu.Unlock()
			return
		default:
		}

		// Update progress
		s.mu.Lock()
		s.activeExport.ExportedTweets = i
		s.activeExport.CurrentFile = fmt.Sprintf("%s (@%s)", tweet.AITitle, tweet.Author.Username)
		s.mu.Unlock()

		exported, size, _, err := s.exportTweet(ctx, tweet, dataDir)
		if err != nil {
			s.logger.Warn("failed to export tweet", "tweet_id", tweet.ID, "error", err)
			continue
		}

		exportedTweets = append(exportedTweets, *exported)

		s.mu.Lock()
		s.activeExport.BytesWritten += size
		s.mu.Unlock()
	}

	// Update phase to finalizing
	s.mu.Lock()
	s.activeExport.Phase = "finalizing"
	s.activeExport.ExportedTweets = len(exportedTweets)
	s.activeExport.CurrentFile = "Writing metadata..."
	s.mu.Unlock()

	// Write tweets-data.json
	tweetsDataPath := filepath.Join(opts.DestPath, "tweets-data.json")
	tweetsData := map[string]interface{}{
		"tweets":      exportedTweets,
		"total":       len(exportedTweets),
		"exported_at": time.Now().UTC(),
		"version":     "1.0",
	}

	tweetsJSON, err := json.MarshalIndent(tweetsData, "", "  ")
	if err != nil {
		s.setExportError(fmt.Sprintf("marshal tweets data: %v", err))
		return
	}

	if err := os.WriteFile(tweetsDataPath, tweetsJSON, 0644); err != nil {
		s.setExportError(fmt.Sprintf("write tweets-data.json: %v", err))
		return
	}

	s.mu.Lock()
	s.activeExport.CurrentFile = "Copying UI..."
	s.mu.Unlock()

	// Copy offline-capable index.html
	if err := s.copyOfflineUI(opts.DestPath); err != nil {
		s.setExportError(fmt.Sprintf("copy offline UI: %v", err))
		return
	}

	// Copy viewer binaries if requested
	if opts.IncludeViewers && opts.ViewerBinDir != "" {
		s.mu.Lock()
		s.activeExport.CurrentFile = "Copying viewer binaries..."
		s.mu.Unlock()

		if err := s.copyViewerBinaries(opts.ViewerBinDir, opts.DestPath); err != nil {
			s.logger.Warn("failed to copy viewer binaries", "error", err)
		}
	}

	// Write README.txt
	if err := s.writeReadme(opts.DestPath, len(exportedTweets)); err != nil {
		s.logger.Warn("failed to write README", "error", err)
	}

	// Mark as completed
	s.mu.Lock()
	s.activeExport.Phase = "completed"
	s.activeExport.CurrentFile = ""
	s.mu.Unlock()

	s.logger.Info("async export complete",
		"tweets", len(exportedTweets),
		"bytes", s.activeExport.BytesWritten,
	)
}

// setExportError sets the export error state.
func (s *ExportService) setExportError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeExport != nil {
		s.activeExport.Phase = "failed"
		s.activeExport.Error = err
	}
}

// GetExportStatus returns the current export status.
func (s *ExportService) GetExportStatus() *ActiveExport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeExport == nil {
		return &ActiveExport{Phase: "idle"}
	}
	// Return a copy to avoid race conditions
	return &ActiveExport{
		ID:             s.activeExport.ID,
		DestPath:       s.activeExport.DestPath,
		Phase:          s.activeExport.Phase,
		TotalTweets:    s.activeExport.TotalTweets,
		ExportedTweets: s.activeExport.ExportedTweets,
		BytesWritten:   s.activeExport.BytesWritten,
		CurrentFile:    s.activeExport.CurrentFile,
		StartedAt:      s.activeExport.StartedAt,
		Error:          s.activeExport.Error,
	}
}

// CancelExport cancels an in-progress export.
func (s *ExportService) CancelExport() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport == nil {
		return fmt.Errorf("no export in progress")
	}

	if s.activeExport.Phase != "preparing" && s.activeExport.Phase != "exporting" && s.activeExport.Phase != "finalizing" {
		return fmt.Errorf("export not in progress (phase: %s)", s.activeExport.Phase)
	}

	if s.activeExport.cancelFunc != nil {
		s.activeExport.cancelFunc()
	}

	return nil
}

// getFreeDiskSpace returns the free disk space at the given path.
func getFreeDiskSpace(path string) int64 {
	// This is a simplified implementation that works on Unix systems
	// For a production implementation, use syscall.Statfs on Unix or GetDiskFreeSpaceEx on Windows
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return 0
	}

	// Try to write a temp file to check if writable
	testFile := filepath.Join(path, ".xgrabba_test")
	f, err := os.Create(testFile)
	if err != nil {
		return 0
	}
	f.Close()
	os.Remove(testFile)

	// Return a placeholder - in production, use proper disk space API
	// For now, return 100GB as a reasonable default for detection
	return 100 * 1024 * 1024 * 1024
}

// ExportOptions configures the export process.
type ExportOptions struct {
	DestPath        string   // Destination directory (e.g., USB drive path)
	IncludeViewers  bool     // Include cross-platform viewer binaries
	ViewerBinDir    string   // Directory containing viewer binaries
	DateRange       *DateRange // Optional date filter
	Authors         []string // Optional author filter
	SearchQuery     string   // Optional search filter
}

// DateRange filters tweets by date.
type DateRange struct {
	Start time.Time
	End   time.Time
}

// ExportProgress tracks export progress.
type ExportProgress struct {
	Phase        string `json:"phase"`
	TotalTweets  int    `json:"total_tweets"`
	ExportedTweets int  `json:"exported_tweets"`
	TotalFiles   int    `json:"total_files"`
	CopiedFiles  int    `json:"copied_files"`
	Error        string `json:"error,omitempty"`
}

// ExportResult contains the result of an export operation.
type ExportResult struct {
	DestPath      string    `json:"dest_path"`
	TweetsCount   int       `json:"tweets_count"`
	MediaCount    int       `json:"media_count"`
	TotalSize     int64     `json:"total_size_bytes"`
	ExportedAt    time.Time `json:"exported_at"`
}

// ExportedTweet is the structure used in tweets-data.json for offline viewing.
type ExportedTweet struct {
	TweetID       string             `json:"tweet_id"`
	URL           string             `json:"url"`
	Author        ExportedAuthor     `json:"author"`
	Text          string             `json:"text"`
	PostedAt      time.Time          `json:"posted_at"`
	ArchivedAt    time.Time          `json:"archived_at"`
	Media         []ExportedMedia    `json:"media"`
	Metrics       domain.TweetMetrics `json:"metrics"`
	AITitle       string             `json:"ai_title"`
	AISummary     string             `json:"ai_summary,omitempty"`
	AITags        []string           `json:"ai_tags,omitempty"`
	AIContentType string             `json:"ai_content_type,omitempty"`
	AITopics      []string           `json:"ai_topics,omitempty"`
	ArchivePath   string             `json:"archive_path"` // Relative path for media lookup
}

// ExportedAuthor contains author info for offline viewing.
type ExportedAuthor struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarPath  string `json:"avatar_path,omitempty"` // Relative path to avatar
	Verified    bool   `json:"verified,omitempty"`
}

// ExportedMedia contains media info for offline viewing.
type ExportedMedia struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	LocalPath          string   `json:"local_path"` // Relative path from archive root
	ThumbnailPath      string   `json:"thumbnail_path,omitempty"`
	Width              int      `json:"width,omitempty"`
	Height             int      `json:"height,omitempty"`
	Duration           int      `json:"duration_seconds,omitempty"`
	AICaption          string   `json:"ai_caption,omitempty"`
	AITags             []string `json:"ai_tags,omitempty"`
	Transcript         string   `json:"transcript,omitempty"`
	TranscriptLanguage string   `json:"transcript_language,omitempty"`
}

// ExportToUSB exports the archive to a USB drive or directory.
func (s *ExportService) ExportToUSB(ctx context.Context, opts ExportOptions) (*ExportResult, error) {
	s.logger.Info("starting export",
		"dest", opts.DestPath,
		"include_viewers", opts.IncludeViewers,
	)

	// Validate destination
	if opts.DestPath == "" {
		return nil, fmt.Errorf("destination path is required")
	}

	// Create destination directory
	if err := os.MkdirAll(opts.DestPath, 0755); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}

	// Get all tweets
	tweets, total, err := s.tweetSvc.List(ctx, 0, 0) // 0 limit = all
	if err != nil {
		return nil, fmt.Errorf("list tweets: %w", err)
	}
	s.logger.Info("found tweets to export", "count", total)

	// Filter tweets if filters are specified
	if opts.DateRange != nil || len(opts.Authors) > 0 || opts.SearchQuery != "" {
		tweets = s.filterTweets(tweets, opts)
		s.logger.Info("filtered tweets", "count", len(tweets))
	}

	// Sort by date (newest first)
	sort.Slice(tweets, func(i, j int) bool {
		return tweets[i].CreatedAt.After(tweets[j].CreatedAt)
	})

	// Create data directory
	dataDir := filepath.Join(opts.DestPath, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	// Export tweets and media
	exportedTweets := make([]ExportedTweet, 0, len(tweets))
	var totalSize int64
	var mediaCount int

	for i, tweet := range tweets {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if i%50 == 0 {
			s.logger.Info("export progress", "exported", i, "total", len(tweets))
		}

		exported, size, count, err := s.exportTweet(ctx, tweet, dataDir)
		if err != nil {
			s.logger.Warn("failed to export tweet", "tweet_id", tweet.ID, "error", err)
			continue
		}

		exportedTweets = append(exportedTweets, *exported)
		totalSize += size
		mediaCount += count
	}

	// Write tweets-data.json
	tweetsDataPath := filepath.Join(opts.DestPath, "tweets-data.json")
	tweetsData := map[string]interface{}{
		"tweets":      exportedTweets,
		"total":       len(exportedTweets),
		"exported_at": time.Now().UTC(),
		"version":     "1.0",
	}

	tweetsJSON, err := json.MarshalIndent(tweetsData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal tweets data: %w", err)
	}

	if err := os.WriteFile(tweetsDataPath, tweetsJSON, 0644); err != nil {
		return nil, fmt.Errorf("write tweets-data.json: %w", err)
	}

	// Copy offline-capable index.html
	if err := s.copyOfflineUI(opts.DestPath); err != nil {
		return nil, fmt.Errorf("copy offline UI: %w", err)
	}

	// Copy viewer binaries if requested
	if opts.IncludeViewers && opts.ViewerBinDir != "" {
		if err := s.copyViewerBinaries(opts.ViewerBinDir, opts.DestPath); err != nil {
			s.logger.Warn("failed to copy viewer binaries", "error", err)
			// Don't fail the export, just log warning
		}
	}

	// Write README.txt
	if err := s.writeReadme(opts.DestPath, len(exportedTweets)); err != nil {
		s.logger.Warn("failed to write README", "error", err)
	}

	result := &ExportResult{
		DestPath:    opts.DestPath,
		TweetsCount: len(exportedTweets),
		MediaCount:  mediaCount,
		TotalSize:   totalSize,
		ExportedAt:  time.Now(),
	}

	s.logger.Info("export complete",
		"tweets", result.TweetsCount,
		"media", result.MediaCount,
		"size_mb", result.TotalSize/(1024*1024),
	)

	return result, nil
}

// filterTweets applies optional filters to the tweet list.
func (s *ExportService) filterTweets(tweets []*domain.Tweet, opts ExportOptions) []*domain.Tweet {
	filtered := make([]*domain.Tweet, 0)

	for _, tweet := range tweets {
		// Date filter
		if opts.DateRange != nil {
			if tweet.PostedAt.Before(opts.DateRange.Start) || tweet.PostedAt.After(opts.DateRange.End) {
				continue
			}
		}

		// Author filter
		if len(opts.Authors) > 0 {
			found := false
			for _, author := range opts.Authors {
				if strings.EqualFold(tweet.Author.Username, author) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Search filter
		if opts.SearchQuery != "" && !s.tweetSvc.tweetMatchesQuery(tweet, strings.ToLower(opts.SearchQuery)) {
			continue
		}

		filtered = append(filtered, tweet)
	}

	return filtered
}

// exportTweet exports a single tweet and its media, returning the exported data and stats.
func (s *ExportService) exportTweet(ctx context.Context, tweet *domain.Tweet, dataDir string) (*ExportedTweet, int64, int, error) {
	// Build relative archive path (YYYY/MM/username_date_tweetID)
	year := tweet.PostedAt.Format("2006")
	month := tweet.PostedAt.Format("01")
	folderName := fmt.Sprintf("%s_%s_%s",
		tweet.Author.Username,
		tweet.PostedAt.Format("2006-01-02"),
		tweet.ID,
	)
	relArchivePath := filepath.Join(year, month, folderName)
	destArchivePath := filepath.Join(dataDir, relArchivePath)

	// Create archive directory
	if err := os.MkdirAll(filepath.Join(destArchivePath, "media"), 0755); err != nil {
		return nil, 0, 0, fmt.Errorf("create archive directory: %w", err)
	}

	var totalSize int64
	var mediaCount int

	// Copy media files
	exportedMedia := make([]ExportedMedia, 0, len(tweet.Media))
	for _, media := range tweet.Media {
		exported, size, err := s.exportMedia(ctx, &media, tweet.ArchivePath, destArchivePath, relArchivePath)
		if err != nil {
			s.logger.Warn("failed to export media", "media_id", media.ID, "error", err)
			continue
		}
		exportedMedia = append(exportedMedia, *exported)
		totalSize += size
		mediaCount++
	}

	// Copy avatar if exists
	var avatarPath string
	srcAvatarPath := filepath.Join(tweet.ArchivePath, "avatar.jpg")
	if _, err := os.Stat(srcAvatarPath); err == nil {
		destAvatarPath := filepath.Join(destArchivePath, "avatar.jpg")
		if size, err := copyFile(srcAvatarPath, destAvatarPath); err == nil {
			avatarPath = filepath.Join("data", relArchivePath, "avatar.jpg")
			totalSize += size
		}
	}

	// Build exported tweet
	archivedAt := time.Now()
	if tweet.ArchivedAt != nil {
		archivedAt = *tweet.ArchivedAt
	}

	exported := &ExportedTweet{
		TweetID:       string(tweet.ID),
		URL:           tweet.URL,
		Author: ExportedAuthor{
			ID:          tweet.Author.ID,
			Username:    tweet.Author.Username,
			DisplayName: tweet.Author.DisplayName,
			AvatarPath:  avatarPath,
			Verified:    tweet.Author.Verified,
		},
		Text:          tweet.Text,
		PostedAt:      tweet.PostedAt,
		ArchivedAt:    archivedAt,
		Media:         exportedMedia,
		Metrics:       tweet.Metrics,
		AITitle:       tweet.AITitle,
		AISummary:     tweet.AISummary,
		AITags:        tweet.AITags,
		AIContentType: tweet.AIContentType,
		AITopics:      tweet.AITopics,
		ArchivePath:   filepath.Join("data", relArchivePath),
	}

	return exported, totalSize, mediaCount, nil
}

// exportMedia exports a single media file.
func (s *ExportService) exportMedia(ctx context.Context, media *domain.Media, srcArchivePath, destArchivePath, relArchivePath string) (*ExportedMedia, int64, error) {
	var totalSize int64

	exported := &ExportedMedia{
		ID:                 media.ID,
		Type:               string(media.Type),
		Width:              media.Width,
		Height:             media.Height,
		Duration:           media.Duration,
		AICaption:          media.AICaption,
		AITags:             media.AITags,
		Transcript:         media.Transcript,
		TranscriptLanguage: media.TranscriptLanguage,
	}

	// Copy main media file
	if media.LocalPath != "" {
		filename := filepath.Base(media.LocalPath)
		srcPath := media.LocalPath
		destPath := filepath.Join(destArchivePath, "media", filename)

		if size, err := copyFile(srcPath, destPath); err == nil {
			exported.LocalPath = filepath.Join("data", relArchivePath, "media", filename)
			totalSize += size
		} else {
			s.logger.Warn("failed to copy media file", "src", srcPath, "error", err)
		}
	}

	// Copy thumbnail for videos
	if media.Type == domain.MediaTypeVideo || media.Type == domain.MediaTypeGIF {
		// Check for thumbnail at the PreviewURL path (which may have been updated to local path)
		thumbFilename := fmt.Sprintf("%s_thumb.jpg", media.ID)
		srcThumbPath := filepath.Join(srcArchivePath, "media", thumbFilename)

		if _, err := os.Stat(srcThumbPath); err == nil {
			destThumbPath := filepath.Join(destArchivePath, "media", thumbFilename)
			if size, err := copyFile(srcThumbPath, destThumbPath); err == nil {
				exported.ThumbnailPath = filepath.Join("data", relArchivePath, "media", thumbFilename)
				totalSize += size
			}
		}
	}

	return exported, totalSize, nil
}

// copyFile copies a file and returns its size.
func copyFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return 0, err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return 0, err
	}

	return srcStat.Size(), nil
}

// copyOfflineUI generates the offline-capable index.html.
// It creates a loader page that fetches tweets-data.json, sets OFFLINE_DATA,
// and then includes the main UI which will detect offline mode automatically.
func (s *ExportService) copyOfflineUI(destPath string) error {
	// Try to read the source index.html for the full UI experience
	// The HTML has offline mode support built-in via OFFLINE_DATA detection
	srcHTMLPath := filepath.Join("internal", "api", "handler", "ui", "index.html")
	srcHTML, err := os.ReadFile(srcHTMLPath)
	if err == nil {
		// Successfully read source - inject offline data loader script
		offlineHTML := injectOfflineDataLoader(string(srcHTML))
		return os.WriteFile(filepath.Join(destPath, "index.html"), []byte(offlineHTML), 0644)
	}

	// Fallback: generate a standalone offline viewer if source not available
	s.logger.Info("source index.html not found, using standalone offline viewer")
	offlineHTML := generateOfflineHTML()
	return os.WriteFile(filepath.Join(destPath, "index.html"), []byte(offlineHTML), 0644)
}

// injectOfflineDataLoader modifies the HTML to load tweets-data.json synchronously before main script
func injectOfflineDataLoader(html string) string {
	// Use synchronous XMLHttpRequest to ensure data is loaded before main script runs
	// This is intentionally synchronous to guarantee OFFLINE_DATA is available
	loaderScript := `<script>
    // Load offline data synchronously before main app initializes
    // Using sync XHR to ensure data is available when main script starts
    (function() {
        try {
            var xhr = new XMLHttpRequest();
            xhr.open('GET', 'tweets-data.json', false); // false = synchronous
            xhr.send(null);
            if (xhr.status === 200) {
                window.OFFLINE_DATA = JSON.parse(xhr.responseText);
                console.log('Loaded offline archive:', window.OFFLINE_DATA.total, 'tweets');
            } else {
                console.error('Failed to load tweets-data.json:', xhr.status);
                window.OFFLINE_DATA = { tweets: [], total: 0 };
            }
        } catch (error) {
            console.error('Failed to load tweets-data.json:', error);
            window.OFFLINE_DATA = { tweets: [], total: 0 };
        }
    })();
</script>
    <script>`

	// Replace the opening <script> tag with our loader + original script
	return strings.Replace(html, "    <script>", loaderScript, 1)
}

// copyViewerBinaries copies the cross-platform viewer binaries.
func (s *ExportService) copyViewerBinaries(binDir, destPath string) error {
	binaries := []struct {
		src  string
		dest string
	}{
		{"xgrabba-viewer.exe", "xgrabba-viewer.exe"},
		{"xgrabba-viewer-mac", "xgrabba-viewer-mac"},
		{"xgrabba-viewer-linux", "xgrabba-viewer-linux"},
	}

	for _, bin := range binaries {
		srcPath := filepath.Join(binDir, bin.src)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue // Skip missing binaries
		}

		destPath := filepath.Join(destPath, bin.dest)
		if _, err := copyFile(srcPath, destPath); err != nil {
			s.logger.Warn("failed to copy viewer binary", "src", bin.src, "error", err)
			continue
		}

		// Make executable on Unix
		os.Chmod(destPath, 0755)
	}

	return nil
}

// writeReadme writes the README.txt file.
func (s *ExportService) writeReadme(destPath string, tweetCount int) error {
	readme := fmt.Sprintf(`XGrabba Archive Export
======================

This archive contains %d archived tweets from X.com (Twitter).

How to View
-----------

Option 1: Use the Viewer Application (Recommended)
- Windows: Double-click xgrabba-viewer.exe
- macOS: Double-click xgrabba-viewer-mac (you may need to right-click and select Open)
- Linux: Run ./xgrabba-viewer-linux from terminal

The viewer will start a local server and open your browser automatically.

Option 2: Use Any Web Server
- Run a local web server in this directory
- For example with Python: python -m http.server 8080
- Then open http://localhost:8080 in your browser

Directory Structure
-------------------

index.html          - The archive viewer application
tweets-data.json    - All tweet metadata in JSON format
data/               - Tweet archives organized by date
  YYYY/MM/          - Year and month folders
    username_date_id/ - Individual tweet archives
      media/        - Images, videos, thumbnails
      avatar.jpg    - Author profile picture

Exported: %s

For more information, visit: https://github.com/iconidentify/xgrabba
`, tweetCount, time.Now().Format("January 2, 2006 at 3:04 PM"))

	return os.WriteFile(filepath.Join(destPath, "README.txt"), []byte(readme), 0644)
}

// generateOfflineHTML generates the offline viewer HTML.
// This is a simplified version that will be replaced with a modified index.html.
func generateOfflineHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>XGrabba Archive Viewer</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #000;
            color: #e7e9ea;
            line-height: 1.5;
        }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 20px 0;
            border-bottom: 1px solid #2f3336;
            margin-bottom: 20px;
        }
        .header h1 { font-size: 24px; font-weight: 700; }
        .stats { color: #71767b; font-size: 14px; }
        .search-box {
            width: 100%;
            padding: 12px 16px;
            background: #202327;
            border: 1px solid #2f3336;
            border-radius: 9999px;
            color: #e7e9ea;
            font-size: 15px;
            margin-bottom: 20px;
        }
        .search-box:focus { outline: none; border-color: #1d9bf0; }
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
            gap: 16px;
        }
        .tweet-card {
            background: #16181c;
            border: 1px solid #2f3336;
            border-radius: 16px;
            overflow: hidden;
            cursor: pointer;
            transition: background 0.2s;
        }
        .tweet-card:hover { background: #1d1f23; }
        .tweet-media {
            width: 100%;
            aspect-ratio: 16/9;
            object-fit: cover;
            background: #202327;
        }
        .tweet-content { padding: 12px; }
        .tweet-author {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 8px;
        }
        .avatar {
            width: 40px;
            height: 40px;
            border-radius: 50%;
            background: #2f3336;
        }
        .author-info { flex: 1; }
        .author-name { font-weight: 700; font-size: 15px; }
        .author-handle { color: #71767b; font-size: 14px; }
        .tweet-text {
            font-size: 15px;
            margin-bottom: 8px;
            display: -webkit-box;
            -webkit-line-clamp: 3;
            -webkit-box-orient: vertical;
            overflow: hidden;
        }
        .tweet-title {
            font-size: 13px;
            color: #1d9bf0;
            margin-bottom: 4px;
        }
        .tweet-tags {
            display: flex;
            flex-wrap: wrap;
            gap: 4px;
            margin-top: 8px;
        }
        .tag {
            background: #1d9bf0;
            color: #fff;
            padding: 2px 8px;
            border-radius: 9999px;
            font-size: 12px;
        }
        .modal {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            background: rgba(0,0,0,0.9);
            z-index: 1000;
            overflow-y: auto;
        }
        .modal.active { display: block; }
        .modal-content {
            max-width: 800px;
            margin: 40px auto;
            background: #16181c;
            border-radius: 16px;
            overflow: hidden;
        }
        .modal-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 16px;
            border-bottom: 1px solid #2f3336;
        }
        .modal-close {
            background: none;
            border: none;
            color: #e7e9ea;
            font-size: 24px;
            cursor: pointer;
        }
        .modal-media {
            width: 100%;
            max-height: 500px;
            object-fit: contain;
            background: #000;
        }
        .modal-body { padding: 16px; }
        .full-text { font-size: 16px; white-space: pre-wrap; margin-bottom: 16px; }
        .metrics {
            display: flex;
            gap: 16px;
            color: #71767b;
            font-size: 14px;
            margin-top: 12px;
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: #71767b;
        }
        .no-results {
            text-align: center;
            padding: 60px 20px;
            color: #71767b;
        }
        .transcript {
            background: #202327;
            padding: 12px;
            border-radius: 8px;
            margin-top: 12px;
            font-size: 14px;
            max-height: 200px;
            overflow-y: auto;
        }
        .transcript-label {
            font-size: 12px;
            color: #71767b;
            margin-bottom: 4px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>XGrabba Archive</h1>
            <div class="stats" id="stats">Loading...</div>
        </div>
        <input type="text" class="search-box" id="search" placeholder="Search tweets, authors, tags...">
        <div class="grid" id="grid"></div>
        <div class="loading" id="loading">Loading archive...</div>
        <div class="no-results" id="no-results" style="display:none;">No tweets found</div>
    </div>

    <div class="modal" id="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span id="modal-title"></span>
                <button class="modal-close" onclick="closeModal()">&times;</button>
            </div>
            <div id="modal-media-container"></div>
            <div class="modal-body" id="modal-body"></div>
        </div>
    </div>

    <script>
        let allTweets = [];
        let filteredTweets = [];

        async function loadData() {
            try {
                const response = await fetch('tweets-data.json');
                const data = await response.json();
                allTweets = data.tweets || [];
                filteredTweets = allTweets;

                document.getElementById('stats').textContent = allTweets.length + ' tweets';
                document.getElementById('loading').style.display = 'none';

                renderTweets();
            } catch (error) {
                document.getElementById('loading').textContent = 'Error loading archive: ' + error.message;
            }
        }

        function renderTweets() {
            const grid = document.getElementById('grid');
            const noResults = document.getElementById('no-results');

            if (filteredTweets.length === 0) {
                grid.innerHTML = '';
                noResults.style.display = 'block';
                return;
            }

            noResults.style.display = 'none';
            grid.innerHTML = filteredTweets.map((tweet, index) => {
                const media = tweet.media && tweet.media[0];
                let mediaHtml = '';

                if (media) {
                    if (media.thumbnail_path) {
                        mediaHtml = '<img class="tweet-media" src="' + media.thumbnail_path + '" alt="">';
                    } else if (media.local_path && media.type === 'image') {
                        mediaHtml = '<img class="tweet-media" src="' + media.local_path + '" alt="">';
                    }
                }

                const tags = (tweet.ai_tags || []).slice(0, 3).map(t =>
                    '<span class="tag">' + escapeHtml(t) + '</span>'
                ).join('');

                return '<div class="tweet-card" onclick="openModal(' + index + ')">' +
                    mediaHtml +
                    '<div class="tweet-content">' +
                        '<div class="tweet-author">' +
                            (tweet.author.avatar_path ?
                                '<img class="avatar" src="' + tweet.author.avatar_path + '" alt="">' :
                                '<div class="avatar"></div>') +
                            '<div class="author-info">' +
                                '<div class="author-name">' + escapeHtml(tweet.author.display_name) + '</div>' +
                                '<div class="author-handle">@' + escapeHtml(tweet.author.username) + '</div>' +
                            '</div>' +
                        '</div>' +
                        (tweet.ai_title ? '<div class="tweet-title">' + escapeHtml(tweet.ai_title) + '</div>' : '') +
                        '<div class="tweet-text">' + escapeHtml(tweet.text) + '</div>' +
                        (tags ? '<div class="tweet-tags">' + tags + '</div>' : '') +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function openModal(index) {
            const tweet = filteredTweets[index];
            const modal = document.getElementById('modal');
            const title = document.getElementById('modal-title');
            const mediaContainer = document.getElementById('modal-media-container');
            const body = document.getElementById('modal-body');

            title.textContent = tweet.ai_title || 'Tweet Details';

            // Media
            let mediaHtml = '';
            if (tweet.media && tweet.media.length > 0) {
                const media = tweet.media[0];
                if (media.type === 'video' || media.type === 'gif') {
                    mediaHtml = '<video class="modal-media" controls src="' + media.local_path + '"></video>';
                } else if (media.type === 'image') {
                    mediaHtml = '<img class="modal-media" src="' + media.local_path + '" alt="">';
                }
            }
            mediaContainer.innerHTML = mediaHtml;

            // Body
            let bodyHtml = '<div class="tweet-author">' +
                (tweet.author.avatar_path ?
                    '<img class="avatar" src="' + tweet.author.avatar_path + '" alt="">' :
                    '<div class="avatar"></div>') +
                '<div class="author-info">' +
                    '<div class="author-name">' + escapeHtml(tweet.author.display_name) + '</div>' +
                    '<div class="author-handle">@' + escapeHtml(tweet.author.username) + '</div>' +
                '</div>' +
            '</div>' +
            '<div class="full-text">' + escapeHtml(tweet.text) + '</div>';

            if (tweet.ai_summary) {
                bodyHtml += '<div style="color:#71767b;font-size:14px;margin-bottom:12px;">AI Summary: ' + escapeHtml(tweet.ai_summary) + '</div>';
            }

            // Transcript
            const media = tweet.media && tweet.media[0];
            if (media && media.transcript) {
                bodyHtml += '<div class="transcript">' +
                    '<div class="transcript-label">Transcript' + (media.transcript_language ? ' (' + media.transcript_language + ')' : '') + '</div>' +
                    escapeHtml(media.transcript) +
                '</div>';
            }

            // Tags
            const allTags = (tweet.ai_tags || []).concat(
                (tweet.media || []).flatMap(m => m.ai_tags || [])
            );
            if (allTags.length > 0) {
                bodyHtml += '<div class="tweet-tags" style="margin-top:12px;">' +
                    allTags.slice(0, 10).map(t => '<span class="tag">' + escapeHtml(t) + '</span>').join('') +
                '</div>';
            }

            bodyHtml += '<div class="metrics">' +
                '<span>' + (tweet.metrics.likes || 0) + ' likes</span>' +
                '<span>' + (tweet.metrics.retweets || 0) + ' retweets</span>' +
                '<span>' + (tweet.metrics.replies || 0) + ' replies</span>' +
            '</div>';

            body.innerHTML = bodyHtml;
            modal.classList.add('active');
        }

        function closeModal() {
            const modal = document.getElementById('modal');
            modal.classList.remove('active');
            // Stop video if playing
            const video = modal.querySelector('video');
            if (video) video.pause();
        }

        function search(query) {
            query = query.toLowerCase().trim();
            if (!query) {
                filteredTweets = allTweets;
            } else {
                filteredTweets = allTweets.filter(tweet => {
                    if (tweet.text.toLowerCase().includes(query)) return true;
                    if (tweet.author.username.toLowerCase().includes(query)) return true;
                    if (tweet.author.display_name.toLowerCase().includes(query)) return true;
                    if ((tweet.ai_title || '').toLowerCase().includes(query)) return true;
                    if ((tweet.ai_summary || '').toLowerCase().includes(query)) return true;
                    if ((tweet.ai_tags || []).some(t => t.toLowerCase().includes(query))) return true;
                    if ((tweet.ai_topics || []).some(t => t.toLowerCase().includes(query))) return true;
                    for (const media of (tweet.media || [])) {
                        if ((media.transcript || '').toLowerCase().includes(query)) return true;
                        if ((media.ai_caption || '').toLowerCase().includes(query)) return true;
                        if ((media.ai_tags || []).some(t => t.toLowerCase().includes(query))) return true;
                    }
                    return false;
                });
            }
            renderTweets();
        }

        function escapeHtml(text) {
            if (!text) return '';
            return text
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
                .replace(/'/g, '&#39;');
        }

        // Event listeners
        document.getElementById('search').addEventListener('input', (e) => search(e.target.value));
        document.getElementById('modal').addEventListener('click', (e) => {
            if (e.target.id === 'modal') closeModal();
        });
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') closeModal();
        });

        // Load data on page load
        loadData();
    </script>
</body>
</html>`
}
