package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/chrisk/xgrabba/internal/config"
	"github.com/chrisk/xgrabba/internal/domain"
	"github.com/chrisk/xgrabba/internal/downloader"
	"github.com/chrisk/xgrabba/internal/repository"
	"github.com/chrisk/xgrabba/pkg/grok"
)

// VideoService orchestrates video archiving workflow.
type VideoService struct {
	videoRepo  *repository.FilesystemVideoRepository
	jobRepo    repository.JobRepository
	grokClient grok.Client
	downloader *downloader.HTTPDownloader
	cfg        config.StorageConfig
	workerCfg  config.WorkerConfig
	logger     *slog.Logger
}

// NewVideoService creates a new video service.
func NewVideoService(
	videoRepo *repository.FilesystemVideoRepository,
	jobRepo repository.JobRepository,
	grokClient grok.Client,
	dl *downloader.HTTPDownloader,
	storageCfg config.StorageConfig,
	workerCfg config.WorkerConfig,
	logger *slog.Logger,
) *VideoService {
	return &VideoService{
		videoRepo:  videoRepo,
		jobRepo:    jobRepo,
		grokClient: grokClient,
		downloader: dl,
		cfg:        storageCfg,
		workerCfg:  workerCfg,
		logger:     logger,
	}
}

// SubmitRequest represents a video submission request.
type SubmitRequest struct {
	TweetURL  string
	TweetID   string
	MediaURLs []string
	Metadata  domain.VideoMetadata
}

// SubmitResponse is returned after submitting a video.
type SubmitResponse struct {
	VideoID domain.VideoID
	JobID   domain.JobID
	Status  domain.VideoStatus
	Message string
}

// StatusResponse contains the current status of a video.
type StatusResponse struct {
	VideoID   domain.VideoID
	Status    domain.VideoStatus
	Filename  string
	FilePath  string
	Error     string
	Progress  string
	CreatedAt time.Time
}

// Submit accepts a new video download request.
func (s *VideoService) Submit(ctx context.Context, req SubmitRequest) (*SubmitResponse, error) {
	// Validate request
	if req.TweetURL == "" {
		return nil, domain.ErrInvalidTweetURL
	}
	if len(req.MediaURLs) == 0 {
		return nil, domain.ErrNoMediaURLs
	}

	// Extract tweet ID if not provided
	if req.TweetID == "" {
		req.TweetID = extractTweetID(req.TweetURL)
	}

	// Check for duplicate
	existing, err := s.videoRepo.GetByTweetID(ctx, req.TweetID)
	if err == nil && existing != nil && existing.Status == domain.StatusCompleted {
		return &SubmitResponse{
			VideoID: existing.ID,
			Status:  existing.Status,
			Message: "Video already archived",
		}, nil
	}

	// Create video entry
	videoID := domain.VideoID("vid_" + uuid.New().String()[:8])
	video := &domain.Video{
		ID:        videoID,
		TweetURL:  req.TweetURL,
		TweetID:   req.TweetID,
		MediaURLs: req.MediaURLs,
		Metadata:  req.Metadata,
		Status:    domain.StatusPending,
		CreatedAt: time.Now(),
	}

	// Set original URLs in metadata
	video.Metadata.OriginalURLs = req.MediaURLs

	// Register video in repository
	s.videoRepo.Register(video)

	// Create job
	jobID := domain.JobID("job_" + uuid.New().String()[:8])
	job := domain.NewJob(jobID, videoID, s.workerCfg.MaxRetries)

	if err := s.jobRepo.Enqueue(ctx, job); err != nil {
		return nil, fmt.Errorf("enqueue job: %w", err)
	}

	s.logger.Info("video submitted",
		"video_id", videoID,
		"job_id", jobID,
		"tweet_url", req.TweetURL,
	)

	return &SubmitResponse{
		VideoID: videoID,
		JobID:   jobID,
		Status:  domain.StatusPending,
		Message: "Video queued for processing",
	}, nil
}

// Process handles the full download and naming workflow.
func (s *VideoService) Process(ctx context.Context, videoID domain.VideoID) error {
	video, err := s.videoRepo.Get(ctx, videoID)
	if err != nil {
		return fmt.Errorf("get video: %w", err)
	}

	logger := s.logger.With("video_id", videoID)

	// Step 1: Download video
	logger.Info("downloading video")
	if err := s.videoRepo.UpdateStatus(ctx, videoID, domain.StatusDownloading, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	content, _, err := s.downloadBestQuality(ctx, video.MediaURLs)
	if err != nil {
		return domain.NewVideoError(videoID, "download", err)
	}
	defer content.Close()

	// Step 2: Generate filename using Grok
	logger.Info("generating filename")
	if err := s.videoRepo.UpdateStatus(ctx, videoID, domain.StatusNaming, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	filename, grokAnalysis := s.generateFilename(ctx, video)
	video.Filename = filename + ".mp4"
	video.Metadata.GrokAnalysis = grokAnalysis

	// Step 3: Build storage path
	video.FilePath = s.videoRepo.BuildStoragePath(video)

	// Step 4: Save video and metadata
	logger.Info("saving video", "path", video.FilePath)
	if err := s.videoRepo.UpdateStatus(ctx, videoID, domain.StatusSaving, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	if err := s.videoRepo.Save(ctx, video, content); err != nil {
		return domain.NewVideoError(videoID, "save", err)
	}

	// Step 5: Mark completed
	now := time.Now()
	video.ProcessedAt = &now
	video.Status = domain.StatusCompleted

	if err := s.videoRepo.UpdateStatus(ctx, videoID, domain.StatusCompleted, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	logger.Info("video archived successfully",
		"filename", video.Filename,
		"path", video.FilePath,
	)

	return nil
}

// GetStatus returns current processing status.
func (s *VideoService) GetStatus(ctx context.Context, videoID domain.VideoID) (*StatusResponse, error) {
	video, err := s.videoRepo.Get(ctx, videoID)
	if err != nil {
		return nil, err
	}

	progress := ""
	switch video.Status {
	case domain.StatusPending:
		progress = "Waiting in queue"
	case domain.StatusDownloading:
		progress = "Downloading video"
	case domain.StatusNaming:
		progress = "Generating filename"
	case domain.StatusSaving:
		progress = "Saving to storage"
	case domain.StatusCompleted:
		progress = "Completed"
	case domain.StatusFailed:
		progress = "Failed"
	}

	return &StatusResponse{
		VideoID:   video.ID,
		Status:    video.Status,
		Filename:  video.Filename,
		FilePath:  video.FilePath,
		Error:     video.Error,
		Progress:  progress,
		CreatedAt: video.CreatedAt,
	}, nil
}

// List returns archived videos.
func (s *VideoService) List(ctx context.Context, status *domain.VideoStatus, limit, offset int) ([]*domain.Video, int, error) {
	videos, err := s.videoRepo.List(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.videoRepo.Count(ctx, status)
	if err != nil {
		return nil, 0, err
	}

	return videos, total, nil
}

func (s *VideoService) downloadBestQuality(ctx context.Context, urls []string) (*readCloserWrapper, int64, error) {
	// Try URLs in order (assuming ordered by quality, highest first)
	var lastErr error
	for _, url := range urls {
		content, size, err := s.downloader.Download(ctx, url)
		if err == nil {
			return &readCloserWrapper{content}, size, nil
		}
		lastErr = err
		s.logger.Debug("download attempt failed", "url", url, "error", err)
	}
	return nil, 0, fmt.Errorf("all download attempts failed: %w", lastErr)
}

func (s *VideoService) generateFilename(ctx context.Context, video *domain.Video) (string, string) {
	// Try Grok first
	filename, err := s.grokClient.GenerateFilename(ctx, grok.FilenameRequest{
		TweetText:      video.Metadata.TweetText,
		AuthorUsername: video.Metadata.AuthorUsername,
		AuthorName:     video.Metadata.AuthorName,
		PostedAt:       video.Metadata.PostedAt.Format("2006-01-02"),
		Duration:       video.Metadata.Duration,
	})

	if err != nil {
		s.logger.Warn("grok filename generation failed, using fallback",
			"video_id", video.ID,
			"error", err,
		)
		// Use fallback
		fallback := grok.FallbackFilename(
			video.Metadata.AuthorUsername,
			video.Metadata.PostedAt,
			video.Metadata.TweetText,
		)
		return fallback, ""
	}

	return filename, filename // Grok analysis is the filename it generated
}

// readCloserWrapper wraps an io.ReadCloser.
type readCloserWrapper struct {
	rc interface {
		Read([]byte) (int, error)
		Close() error
	}
}

func (w *readCloserWrapper) Read(p []byte) (int, error) {
	return w.rc.Read(p)
}

func (w *readCloserWrapper) Close() error {
	return w.rc.Close()
}

// extractTweetID extracts the tweet ID from a tweet URL.
func extractTweetID(url string) string {
	// Match patterns like:
	// https://x.com/user/status/1234567890
	// https://twitter.com/user/status/1234567890
	re := regexp.MustCompile(`(?:twitter\.com|x\.com)/\w+/status/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
