package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/downloader"
	"github.com/iconidentify/xgrabba/pkg/ffmpeg"
	"github.com/iconidentify/xgrabba/pkg/grok"
	"github.com/iconidentify/xgrabba/pkg/twitter"
	"github.com/iconidentify/xgrabba/pkg/whisper"
)

// TweetService orchestrates tweet archiving workflow.
type TweetService struct {
	twitterClient  *twitter.Client
	grokClient     grok.Client
	whisperClient  *whisper.HTTPClient
	videoProcessor *ffmpeg.VideoProcessor
	downloader     *downloader.HTTPDownloader
	cfg            config.StorageConfig
	whisperEnabled bool
	logger         *slog.Logger

	// In-memory storage (could be replaced with DB)
	tweets map[domain.TweetID]*domain.Tweet

	// Mutex to prevent duplicate AI analysis
	aiAnalysisLock sync.Mutex
	processingAI   map[domain.TweetID]bool // Track which tweets are currently being analyzed
}

// NewTweetService creates a new tweet service.
func NewTweetService(
	grokClient grok.Client,
	whisperClient *whisper.HTTPClient,
	dl *downloader.HTTPDownloader,
	storageCfg config.StorageConfig,
	whisperEnabled bool,
	logger *slog.Logger,
) *TweetService {
	// Initialize video processor (ffmpeg)
	var videoProc *ffmpeg.VideoProcessor
	if ffmpeg.IsAvailable() {
		var err error
		videoProc, err = ffmpeg.NewVideoProcessor()
		if err != nil {
			logger.Warn("failed to initialize video processor", "error", err)
		} else {
			version, _ := ffmpeg.GetVersion()
			logger.Info("video processor initialized", "ffmpeg_version", version)
		}
	} else {
		logger.Warn("ffmpeg not available, video transcription disabled")
	}

	svc := &TweetService{
		twitterClient:  twitter.NewClient(),
		grokClient:     grokClient,
		whisperClient:  whisperClient,
		videoProcessor: videoProc,
		downloader:     dl,
		cfg:            storageCfg,
		whisperEnabled: whisperEnabled && whisperClient != nil && videoProc != nil,
		logger:         logger,
		tweets:         make(map[domain.TweetID]*domain.Tweet),
		processingAI:   make(map[domain.TweetID]bool),
	}

	// Load existing tweets from disk on startup
	if err := svc.LoadFromDisk(); err != nil {
		logger.Warn("failed to load existing tweets from disk", "error", err)
	}

	return svc
}

// LoadFromDisk scans the storage directory and loads existing archived tweets.
func (s *TweetService) LoadFromDisk() error {
	s.logger.Info("scanning storage for existing archives", "path", s.cfg.BasePath)

	count := 0
	err := filepath.Walk(s.cfg.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}
		if info.IsDir() || info.Name() != "tweet.json" {
			return nil
		}

		// Read the tweet.json file
		data, err := os.ReadFile(path)
		if err != nil {
			s.logger.Warn("failed to read tweet.json", "path", path, "error", err)
			return nil
		}

		var stored domain.StoredTweet
		if err := json.Unmarshal(data, &stored); err != nil {
			s.logger.Warn("failed to parse tweet.json", "path", path, "error", err)
			return nil
		}

		// Convert StoredTweet back to Tweet
		tweet := s.storedTweetToTweet(&stored, filepath.Dir(path))
		s.tweets[tweet.ID] = tweet
		count++

		return nil
	})

	s.logger.Info("loaded existing tweets", "count", count)
	return err
}

// storedTweetToTweet converts a StoredTweet from disk back to a Tweet.
func (s *TweetService) storedTweetToTweet(stored *domain.StoredTweet, archivePath string) *domain.Tweet {
	tweet := &domain.Tweet{
		ID:            domain.TweetID(stored.TweetID),
		URL:           stored.URL,
		Author:        stored.Author,
		Text:          stored.Text,
		PostedAt:      stored.PostedAt,
		Media:         stored.Media,
		Metrics:       stored.Metrics,
		Status:        domain.ArchiveStatusCompleted,
		ArchivePath:   archivePath,
		AITitle:       stored.AITitle,
		AISummary:     stored.AISummary,
		AITags:        stored.AITags,
		AIContentType: stored.AIContentType,
		AITopics:      stored.AITopics,
		CreatedAt:     stored.ArchivedAt,
		ArchivedAt:    &stored.ArchivedAt,
	}

	if stored.ReplyTo != "" {
		replyTo := domain.TweetID(stored.ReplyTo)
		tweet.ReplyTo = &replyTo
	}
	if stored.QuotedTweet != "" {
		quoted := domain.TweetID(stored.QuotedTweet)
		tweet.QuotedTweet = &quoted
	}

	return tweet
}

// BackfillAIMetadata processes existing tweets that are missing AI analysis.
// This runs in the background and doesn't block startup.
func (s *TweetService) BackfillAIMetadata(ctx context.Context) {
	var needsBackfill []*domain.Tweet

	for _, tweet := range s.tweets {
		// Check if tweet is missing AI metadata
		if len(tweet.AITags) == 0 && tweet.AISummary == "" {
			needsBackfill = append(needsBackfill, tweet)
		}
	}

	if len(needsBackfill) == 0 {
		s.logger.Info("no tweets need AI metadata backfill")
		return
	}

	s.logger.Info("starting AI metadata backfill", "count", len(needsBackfill))

	for i, tweet := range needsBackfill {
		select {
		case <-ctx.Done():
			s.logger.Info("backfill cancelled", "processed", i)
			return
		default:
		}

		s.logger.Info("backfilling AI metadata", "tweet_id", tweet.ID, "progress", fmt.Sprintf("%d/%d", i+1, len(needsBackfill)))

		// Run vision analysis (will fall back to text if no media)
		s.runVisionAnalysis(ctx, tweet)

		// Save updated metadata to disk
		if err := s.saveTweetMetadata(tweet); err != nil {
			s.logger.Warn("failed to save backfilled metadata", "tweet_id", tweet.ID, "error", err)
			continue
		}

		s.logger.Info("backfilled tweet", "tweet_id", tweet.ID, "tags_count", len(tweet.AITags))

		// Small delay to avoid rate limiting
		time.Sleep(1 * time.Second) // Slightly longer for vision API
	}

	s.logger.Info("AI metadata backfill complete", "processed", len(needsBackfill))
}

// ArchiveRequest represents a tweet archive request.
type ArchiveRequest struct {
	TweetURL string
}

// ArchiveResponse is returned after submitting an archive request.
type ArchiveResponse struct {
	TweetID domain.TweetID
	Status  domain.ArchiveStatus
	Message string
}

// TweetStatusResponse contains the current status of a tweet archive.
type TweetStatusResponse struct {
	TweetID     domain.TweetID
	Status      domain.ArchiveStatus
	Author      string
	Text        string
	MediaCount  int
	AITitle     string
	ArchivePath string
	Error       string
	CreatedAt   time.Time
}

// Archive submits a tweet URL for archiving.
func (s *TweetService) Archive(ctx context.Context, req ArchiveRequest) (*ArchiveResponse, error) {
	s.logger.Info("archive request received", "url", req.TweetURL)

	// Extract tweet ID
	tweetID := twitter.ExtractTweetID(req.TweetURL)
	if tweetID == "" {
		return nil, domain.ErrInvalidTweetURL
	}

	// Check if already archived
	if existing, ok := s.tweets[domain.TweetID(tweetID)]; ok {
		if existing.Status == domain.ArchiveStatusCompleted {
			return &ArchiveResponse{
				TweetID: existing.ID,
				Status:  existing.Status,
				Message: "Tweet already archived",
			}, nil
		}
	}

	// Create initial tweet record
	tweet := &domain.Tweet{
		ID:        domain.TweetID(tweetID),
		URL:       req.TweetURL,
		Status:    domain.ArchiveStatusPending,
		CreatedAt: time.Now(),
	}
	s.tweets[tweet.ID] = tweet

	// Process asynchronously
	go s.processTweet(context.Background(), tweet)

	return &ArchiveResponse{
		TweetID: tweet.ID,
		Status:  domain.ArchiveStatusPending,
		Message: "Tweet queued for archiving",
	}, nil
}

// processTweet handles the full archive workflow.
func (s *TweetService) processTweet(ctx context.Context, tweet *domain.Tweet) {
	logger := s.logger.With("tweet_id", tweet.ID)

	// Step 1: Fetch tweet data
	logger.Info("fetching tweet data")
	tweet.Status = domain.ArchiveStatusFetching

	fetchedTweet, err := s.twitterClient.FetchTweet(ctx, tweet.URL)
	if err != nil {
		logger.Error("failed to fetch tweet", "error", err)
		tweet.Status = domain.ArchiveStatusFailed
		tweet.Error = fmt.Sprintf("Failed to fetch tweet: %v", err)
		return
	}

	// Merge fetched data into our tweet record
	tweet.Author = fetchedTweet.Author
	tweet.Text = fetchedTweet.Text
	tweet.PostedAt = fetchedTweet.PostedAt
	tweet.Media = fetchedTweet.Media
	tweet.Metrics = fetchedTweet.Metrics
	tweet.ReplyTo = fetchedTweet.ReplyTo
	tweet.QuotedTweet = fetchedTweet.QuotedTweet

	logger.Info("tweet fetched",
		"author", tweet.Author.Username,
		"media_count", len(tweet.Media),
		"has_video", tweet.HasVideo(),
	)

	// Step 2: Generate AI title
	logger.Info("generating AI title")
	tweet.Status = domain.ArchiveStatusProcessing

	aiTitle, aiSummary := s.generateAIMetadata(ctx, tweet)
	tweet.AITitle = aiTitle
	tweet.AISummary = aiSummary

	// Step 3: Create archive directory
	archivePath := s.buildArchivePath(tweet)
	tweet.ArchivePath = archivePath

	if err := os.MkdirAll(filepath.Join(archivePath, "media"), 0755); err != nil {
		logger.Error("failed to create archive directory", "error", err)
		tweet.Status = domain.ArchiveStatusFailed
		tweet.Error = fmt.Sprintf("Failed to create directory: %v", err)
		return
	}

	// Step 4: Download all media
	if len(tweet.Media) > 0 {
		logger.Info("downloading media", "count", len(tweet.Media))
		tweet.Status = domain.ArchiveStatusDownloading

		for i := range tweet.Media {
			if err := s.downloadMedia(ctx, tweet, &tweet.Media[i], archivePath); err != nil {
				logger.Warn("failed to download media",
					"media_id", tweet.Media[i].ID,
					"error", err,
				)
				// Continue with other media
			}
		}
	}

	// Step 4b: Download author avatar
	if tweet.Author.AvatarURL != "" {
		avatarPath := filepath.Join(archivePath, "avatar.jpg")
		if err := s.downloadThumbnail(ctx, tweet.Author.AvatarURL, avatarPath); err != nil {
			logger.Warn("failed to download author avatar", "error", err)
		} else {
			tweet.Author.LocalAvatarURL = avatarPath
		}
	}

	// Step 5: Save tweet metadata
	logger.Info("saving tweet metadata")
	if err := s.saveTweetMetadata(tweet); err != nil {
		logger.Error("failed to save metadata", "error", err)
		tweet.Status = domain.ArchiveStatusFailed
		tweet.Error = fmt.Sprintf("Failed to save metadata: %v", err)
		return
	}

	// Done!
	now := time.Now()
	tweet.ArchivedAt = &now
	tweet.Status = domain.ArchiveStatusCompleted

	logger.Info("tweet archived successfully",
		"path", archivePath,
		"ai_title", tweet.AITitle,
	)
}

func (s *TweetService) generateAIMetadata(ctx context.Context, tweet *domain.Tweet) (string, string) {
	// Build prompt for Grok
	prompt := buildTweetPrompt(tweet)

	title, err := s.grokClient.GenerateFilename(ctx, grok.FilenameRequest{
		TweetText:      prompt,
		AuthorUsername: tweet.Author.Username,
		AuthorName:     tweet.Author.DisplayName,
		PostedAt:       tweet.PostedAt.Format("2006-01-02"),
		Duration:       getTotalVideoDuration(tweet),
	})

	if err != nil {
		s.logger.Warn("AI title generation failed, using fallback", "error", err)
		// Fallback: use author + first few words
		title = grok.FallbackFilename(tweet.Author.Username, tweet.PostedAt, tweet.Text)
	}

	// Perform vision-based content analysis if we have media
	// This extracts text from images, identifies objects, people, etc.
	s.runVisionAnalysis(ctx, tweet)

	return title, ""
}

// runVisionAnalysis performs AI analysis on the tweet's media content.
// If media files are available locally, it uses vision analysis for rich metadata extraction.
func (s *TweetService) runVisionAnalysis(ctx context.Context, tweet *domain.Tweet) {
	// Collect local image paths, video thumbnails, and extracted keyframes
	var imagePaths []string
	var videoThumbPath string

	for _, m := range tweet.Media {
		if m.LocalPath == "" {
			continue
		}
		switch m.Type {
		case domain.MediaTypeImage:
			imagePaths = append(imagePaths, m.LocalPath)
		case domain.MediaTypeVideo, domain.MediaTypeGIF:
			// For videos, use the thumbnail if available
			if m.PreviewURL != "" && filepath.IsAbs(m.PreviewURL) {
				videoThumbPath = m.PreviewURL
			}
			// Also include extracted keyframes if they exist
			keyframesDir := filepath.Join(tweet.ArchivePath, "media", "keyframes_"+m.ID)
			if entries, err := os.ReadDir(keyframesDir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".jpg" || filepath.Ext(entry.Name()) == ".jpeg") {
						framePath := filepath.Join(keyframesDir, entry.Name())
						imagePaths = append(imagePaths, framePath)
					}
				}
			}
		}
	}

	// If we have images or video thumbnail, use vision analysis
	if len(imagePaths) > 0 || videoThumbPath != "" {
		s.logger.Info("running vision analysis",
			"tweet_id", tweet.ID,
			"images", len(imagePaths),
			"has_video_thumb", videoThumbPath != "",
		)

		analysis, err := s.grokClient.AnalyzeContentWithVision(ctx, grok.VisionAnalysisRequest{
			TweetText:      tweet.Text,
			AuthorUsername: tweet.Author.Username,
			ImagePaths:     imagePaths,
			VideoThumbPath: videoThumbPath,
			HasVideo:       tweet.HasVideo(),
			VideoDuration:  getTotalVideoDuration(tweet),
		})

		if err != nil {
			s.logger.Warn("vision analysis failed, falling back to text analysis", "error", err)
			// Fall back to text-only analysis
			s.runTextAnalysis(ctx, tweet)
			return
		}

		tweet.AISummary = analysis.Summary
		tweet.AITags = analysis.Tags
		tweet.AIContentType = analysis.ContentType
		tweet.AITopics = analysis.Topics
		s.logger.Info("vision analysis complete",
			"tags_count", len(analysis.Tags),
			"content_type", analysis.ContentType,
		)
		return
	}

	// No media available, use text-only analysis
	s.runTextAnalysis(ctx, tweet)
}

// runTextAnalysis performs text-only AI analysis (no vision).
func (s *TweetService) runTextAnalysis(ctx context.Context, tweet *domain.Tweet) {
	analysis, err := s.grokClient.AnalyzeContent(ctx, grok.ContentAnalysisRequest{
		TweetText:      tweet.Text,
		AuthorUsername: tweet.Author.Username,
		HasVideo:       tweet.HasVideo(),
		HasImages:      tweet.HasImages(),
		ImageCount:     countImages(tweet),
		VideoDuration:  getTotalVideoDuration(tweet),
	})

	if err != nil {
		s.logger.Warn("AI content analysis failed", "error", err)
		return
	}

	tweet.AISummary = analysis.Summary
	tweet.AITags = analysis.Tags
	tweet.AIContentType = analysis.ContentType
	tweet.AITopics = analysis.Topics
	s.logger.Info("text analysis complete",
		"tags_count", len(analysis.Tags),
		"content_type", analysis.ContentType,
	)
}

// RegenerateAIMetadata re-runs AI analysis on a tweet and updates its metadata.
// This is useful when the AI algorithm is improved or to get better results.
// Returns an error if analysis is already in progress for this tweet.
func (s *TweetService) RegenerateAIMetadata(ctx context.Context, tweetID domain.TweetID) error {
	// Check if already processing
	s.aiAnalysisLock.Lock()
	if s.processingAI[tweetID] {
		s.aiAnalysisLock.Unlock()
		return fmt.Errorf("AI analysis already in progress for this tweet")
	}
	s.processingAI[tweetID] = true
	s.aiAnalysisLock.Unlock()

	// Ensure we clear the processing flag when done
	defer func() {
		s.aiAnalysisLock.Lock()
		delete(s.processingAI, tweetID)
		s.aiAnalysisLock.Unlock()
	}()

	tweet, ok := s.tweets[tweetID]
	if !ok {
		return domain.ErrVideoNotFound
	}

	s.logger.Info("regenerating AI metadata", "tweet_id", tweetID)

	// Clear existing AI metadata
	tweet.AISummary = ""
	tweet.AITags = nil
	tweet.AIContentType = ""
	tweet.AITopics = nil

	// Re-run transcription for videos if Whisper is enabled
	if s.whisperEnabled && tweet.HasVideo() {
		s.logger.Info("re-running video transcription", "tweet_id", tweetID)
		for i := range tweet.Media {
			media := &tweet.Media[i]
			if (media.Type == domain.MediaTypeVideo || media.Type == domain.MediaTypeGIF) && media.LocalPath != "" {
				// Clear existing transcript
				media.Transcript = ""
				media.TranscriptLanguage = ""
				// Re-run transcription
				s.processVideoForTranscription(ctx, media, tweet.ArchivePath)
			}
		}
	}

	// Re-run vision analysis (this will also extract keyframes if needed)
	s.runVisionAnalysis(ctx, tweet)

	// Also regenerate the title
	prompt := buildTweetPrompt(tweet)
	title, err := s.grokClient.GenerateFilename(ctx, grok.FilenameRequest{
		TweetText:      prompt,
		AuthorUsername: tweet.Author.Username,
		AuthorName:     tweet.Author.DisplayName,
		PostedAt:       tweet.PostedAt.Format("2006-01-02"),
		Duration:       getTotalVideoDuration(tweet),
	})
	if err == nil {
		tweet.AITitle = title
	}

	// Save updated metadata to disk
	if err := s.saveTweetMetadata(tweet); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}

	s.logger.Info("AI metadata regenerated",
		"tweet_id", tweetID,
		"tags_count", len(tweet.AITags),
		"summary_len", len(tweet.AISummary),
	)

	return nil
}

// IsAIAnalysisInProgress checks if AI analysis is currently running for a tweet.
func (s *TweetService) IsAIAnalysisInProgress(tweetID domain.TweetID) bool {
	s.aiAnalysisLock.Lock()
	defer s.aiAnalysisLock.Unlock()
	return s.processingAI[tweetID]
}

func buildTweetPrompt(tweet *domain.Tweet) string {
	var sb strings.Builder
	sb.WriteString(tweet.Text)

	if tweet.HasVideo() {
		sb.WriteString("\n[Contains video]")
	}
	if tweet.HasImages() {
		sb.WriteString(fmt.Sprintf("\n[Contains %d images]", countImages(tweet)))
	}

	return sb.String()
}

func getTotalVideoDuration(tweet *domain.Tweet) int {
	total := 0
	for _, m := range tweet.Media {
		if m.Type == domain.MediaTypeVideo {
			total += m.Duration
		}
	}
	return total
}

func countImages(tweet *domain.Tweet) int {
	count := 0
	for _, m := range tweet.Media {
		if m.Type == domain.MediaTypeImage {
			count++
		}
	}
	return count
}

func (s *TweetService) buildArchivePath(tweet *domain.Tweet) string {
	year := tweet.PostedAt.Format("2006")
	month := tweet.PostedAt.Format("01")

	// Create a readable folder name
	folderName := fmt.Sprintf("%s_%s_%s",
		tweet.Author.Username,
		tweet.PostedAt.Format("2006-01-02"),
		tweet.ID,
	)

	return filepath.Join(s.cfg.BasePath, year, month, folderName)
}

func (s *TweetService) downloadMedia(ctx context.Context, tweet *domain.Tweet, media *domain.Media, archivePath string) error {
	// Determine filename
	var filename string
	switch media.Type {
	case domain.MediaTypeImage:
		ext := ".jpg"
		if strings.Contains(media.URL, ".png") {
			ext = ".png"
		} else if strings.Contains(media.URL, ".webp") {
			ext = ".webp"
		}
		filename = fmt.Sprintf("%s%s", media.ID, ext)
	case domain.MediaTypeVideo, domain.MediaTypeGIF:
		filename = fmt.Sprintf("%s.mp4", media.ID)
	}

	localPath := filepath.Join(archivePath, "media", filename)

	// Download main media file
	content, _, err := s.downloader.Download(ctx, media.URL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer content.Close()

	// Save to file
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, content); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	media.LocalPath = localPath
	media.Downloaded = true

	// For videos, also download the thumbnail/preview image
	if (media.Type == domain.MediaTypeVideo || media.Type == domain.MediaTypeGIF) && media.PreviewURL != "" {
		thumbPath := filepath.Join(archivePath, "media", fmt.Sprintf("%s_thumb.jpg", media.ID))
		if err := s.downloadThumbnail(ctx, media.PreviewURL, thumbPath); err != nil {
			s.logger.Warn("failed to download video thumbnail", "media_id", media.ID, "error", err)
			// Continue anyway - thumbnail is optional
		} else {
			// Update PreviewURL to point to local path for later reference
			media.PreviewURL = thumbPath
		}
	}

	// For videos, extract keyframes and transcribe audio
	if (media.Type == domain.MediaTypeVideo || media.Type == domain.MediaTypeGIF) && s.whisperEnabled {
		s.processVideoForTranscription(ctx, media, archivePath)
	}

	return nil
}

// processVideoForTranscription extracts keyframes and audio from a video and transcribes it.
func (s *TweetService) processVideoForTranscription(ctx context.Context, media *domain.Media, archivePath string) {
	if s.videoProcessor == nil || s.whisperClient == nil {
		return
	}

	logger := s.logger.With("media_id", media.ID)
	logger.Info("processing video for transcription")

	// Create temp directory for processing
	tempDir := filepath.Join(archivePath, "temp_processing")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		logger.Warn("failed to create temp directory", "error", err)
		return
	}
	defer os.RemoveAll(tempDir) // Clean up temp files

	// Extract keyframes for vision analysis
	framesDir := filepath.Join(tempDir, "frames")
	frames, err := s.videoProcessor.ExtractKeyframes(ctx, media.LocalPath, ffmpeg.ExtractKeyframesConfig{
		IntervalSeconds: 10,
		MaxFrames:       10, // Limit for API
		MaxWidth:        1280,
		Quality:         5,
		OutputDir:       framesDir,
	})
	if err != nil {
		logger.Warn("failed to extract keyframes", "error", err)
	} else {
		logger.Info("extracted keyframes", "count", len(frames))

		// Copy keyframes to media directory for permanent storage
		keyframesDir := filepath.Join(archivePath, "media", "keyframes_"+media.ID)
		if err := os.MkdirAll(keyframesDir, 0755); err == nil {
			for i, framePath := range frames {
				destPath := filepath.Join(keyframesDir, fmt.Sprintf("frame_%03d.jpg", i))
				if data, err := os.ReadFile(framePath); err == nil {
					os.WriteFile(destPath, data, 0644)
				}
			}
		}
	}

	// Extract audio for transcription
	audioPath := filepath.Join(tempDir, "audio.mp3")
	_, audioDuration, err := s.videoProcessor.ExtractAudio(ctx, media.LocalPath, ffmpeg.ExtractAudioConfig{
		OutputPath: audioPath,
		Format:     "mp3",
		SampleRate: 16000,
		Channels:   1,
		Bitrate:    "64k",
	})
	if err != nil {
		logger.Warn("failed to extract audio", "error", err)
		return
	}

	logger.Info("extracted audio", "duration_seconds", audioDuration)

	// Check if audio needs to be chunked
	audioStat, err := os.Stat(audioPath)
	if err != nil {
		logger.Warn("failed to stat audio file", "error", err)
		return
	}

	var transcription *whisper.TranscriptionResponse

	if audioStat.Size() > 20*1024*1024 { // Over 20MB, needs chunking
		logger.Info("audio file large, chunking for transcription", "size_mb", audioStat.Size()/(1024*1024))

		chunks, err := s.videoProcessor.ChunkAudio(ctx, audioPath, ffmpeg.ChunkAudioConfig{
			ChunkDurationSec: 300, // 5 minutes per chunk
			OutputDir:        filepath.Join(tempDir, "chunks"),
			Format:           "mp3",
		})
		if err != nil {
			logger.Warn("failed to chunk audio", "error", err)
			return
		}

		transcription, err = s.whisperClient.TranscribeChunks(ctx, chunks, whisper.TranscriptionOptions{})
		if err != nil {
			logger.Warn("failed to transcribe audio chunks", "error", err)
			return
		}
	} else {
		// Transcribe directly
		transcription, err = s.whisperClient.TranscribeFile(ctx, audioPath, whisper.TranscriptionOptions{})
		if err != nil {
			logger.Warn("failed to transcribe audio", "error", err)
			return
		}
	}

	// Store transcript in media
	media.Transcript = transcription.Text
	media.TranscriptLanguage = transcription.Language

	logger.Info("transcription complete",
		"transcript_length", len(transcription.Text),
		"language", transcription.Language,
	)

	// Save transcript to file as well
	transcriptPath := filepath.Join(archivePath, "media", fmt.Sprintf("%s_transcript.txt", media.ID))
	if err := os.WriteFile(transcriptPath, []byte(transcription.Text), 0644); err != nil {
		logger.Warn("failed to save transcript file", "error", err)
	}
}

// downloadThumbnail downloads a thumbnail image to the specified path.
func (s *TweetService) downloadThumbnail(ctx context.Context, url, destPath string) error {
	content, _, err := s.downloader.Download(ctx, url)
	if err != nil {
		return fmt.Errorf("download thumbnail: %w", err)
	}
	defer content.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create thumbnail file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, content); err != nil {
		return fmt.Errorf("write thumbnail: %w", err)
	}

	return nil
}

func (s *TweetService) saveTweetMetadata(tweet *domain.Tweet) error {
	stored := tweet.ToStoredTweet()

	// Save as JSON
	jsonPath := filepath.Join(tweet.ArchivePath, "tweet.json")
	data, err := jsonMarshalIndent(stored)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	// Also save a human-readable markdown summary
	mdPath := filepath.Join(tweet.ArchivePath, "README.md")
	md := buildMarkdownSummary(tweet)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return fmt.Errorf("write markdown: %w", err)
	}

	return nil
}

func buildMarkdownSummary(tweet *domain.Tweet) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", tweet.AITitle))
	sb.WriteString(fmt.Sprintf("**Author:** @%s (%s)\n\n", tweet.Author.Username, tweet.Author.DisplayName))
	sb.WriteString(fmt.Sprintf("**Posted:** %s\n\n", tweet.PostedAt.Format("January 2, 2006 at 3:04 PM")))
	sb.WriteString(fmt.Sprintf("**Original URL:** %s\n\n", tweet.URL))
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", tweet.Text))

	if len(tweet.Media) > 0 {
		sb.WriteString("---\n\n## Media\n\n")
		for _, m := range tweet.Media {
			if m.LocalPath != "" {
				relPath := filepath.Base(m.LocalPath)
				if m.Type == domain.MediaTypeImage {
					sb.WriteString(fmt.Sprintf("![Image](media/%s)\n\n", relPath))
				} else {
					sb.WriteString(fmt.Sprintf("- [Video: %s](media/%s)\n", relPath, relPath))
				}
			}
		}
	}

	sb.WriteString("\n---\n\n## Metrics\n\n")
	sb.WriteString(fmt.Sprintf("- Likes: %d\n", tweet.Metrics.Likes))
	sb.WriteString(fmt.Sprintf("- Retweets: %d\n", tweet.Metrics.Retweets))
	sb.WriteString(fmt.Sprintf("- Replies: %d\n", tweet.Metrics.Replies))

	archivedAt := time.Now()
	if tweet.ArchivedAt != nil {
		archivedAt = *tweet.ArchivedAt
	}
	sb.WriteString(fmt.Sprintf("\n---\n\n*Archived on %s by XGrabba*\n", archivedAt.Format("January 2, 2006")))

	return sb.String()
}

// GetStatus returns the current status of a tweet archive.
func (s *TweetService) GetStatus(ctx context.Context, tweetID domain.TweetID) (*TweetStatusResponse, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return nil, domain.ErrVideoNotFound
	}

	return &TweetStatusResponse{
		TweetID:     tweet.ID,
		Status:      tweet.Status,
		Author:      tweet.Author.Username,
		Text:        truncateText(tweet.Text, 100),
		MediaCount:  len(tweet.Media),
		AITitle:     tweet.AITitle,
		ArchivePath: tweet.ArchivePath,
		Error:       tweet.Error,
		CreatedAt:   tweet.CreatedAt,
	}, nil
}

// List returns archived tweets sorted by date (newest first).
func (s *TweetService) List(ctx context.Context, limit, offset int) ([]*domain.Tweet, int, error) {
	var result []*domain.Tweet
	for _, tweet := range s.tweets {
		result = append(result, tweet)
	}

	// Sort by CreatedAt descending (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	total := len(result)

	// Apply pagination
	if offset >= len(result) {
		return []*domain.Tweet{}, total, nil
	}
	result = result[offset:]
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, total, nil
}

// Delete removes a tweet archive including all files.
func (s *TweetService) Delete(ctx context.Context, tweetID domain.TweetID) error {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return domain.ErrVideoNotFound
	}

	// Delete the archive directory if it exists
	if tweet.ArchivePath != "" {
		if err := os.RemoveAll(tweet.ArchivePath); err != nil {
			s.logger.Warn("failed to delete archive directory",
				"tweet_id", tweetID,
				"path", tweet.ArchivePath,
				"error", err,
			)
			// Continue anyway to remove from memory
		}
	}

	// Remove from in-memory storage
	delete(s.tweets, tweetID)

	s.logger.Info("tweet deleted", "tweet_id", tweetID)
	return nil
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func jsonMarshalIndent(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// MediaFile represents a media file in the archive.
type MediaFile struct {
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// GetFullTweet returns complete tweet details from the stored JSON.
func (s *TweetService) GetFullTweet(ctx context.Context, tweetID domain.TweetID) (*domain.StoredTweet, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return nil, domain.ErrVideoNotFound
	}

	// Read the tweet.json file for complete data
	jsonPath := filepath.Join(tweet.ArchivePath, "tweet.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		// If file doesn't exist yet (still processing), return what we have
		if os.IsNotExist(err) {
			stored := tweet.ToStoredTweet()
			return &stored, nil
		}
		return nil, fmt.Errorf("read tweet.json: %w", err)
	}

	var stored domain.StoredTweet
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal tweet.json: %w", err)
	}

	return &stored, nil
}

// ListMediaFiles returns list of media files for a tweet.
func (s *TweetService) ListMediaFiles(ctx context.Context, tweetID domain.TweetID) ([]MediaFile, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return nil, domain.ErrVideoNotFound
	}

	mediaDir := filepath.Join(tweet.ArchivePath, "media")
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []MediaFile{}, nil
		}
		return nil, fmt.Errorf("read media directory: %w", err)
	}

	files := make([]MediaFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		filename := entry.Name()
		files = append(files, MediaFile{
			Filename:    filename,
			Type:        getMediaType(filename),
			Size:        info.Size(),
			ContentType: getContentType(filename),
		})
	}

	return files, nil
}

// GetMediaFilePath returns the full filesystem path to a media file.
func (s *TweetService) GetMediaFilePath(ctx context.Context, tweetID domain.TweetID, filename string) (string, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return "", domain.ErrVideoNotFound
	}

	// Security: validate filename to prevent path traversal
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return "", domain.ErrMediaNotFound
	}

	filePath := filepath.Join(tweet.ArchivePath, "media", filename)

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", domain.ErrMediaNotFound
	}

	return filePath, nil
}

// GetArchivePath returns the archive path for a tweet.
func (s *TweetService) GetArchivePath(ctx context.Context, tweetID domain.TweetID) (string, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return "", domain.ErrVideoNotFound
	}
	return tweet.ArchivePath, nil
}

// GetAvatarPath returns the path to the locally stored avatar for a tweet's author.
func (s *TweetService) GetAvatarPath(ctx context.Context, tweetID domain.TweetID) (string, error) {
	tweet, ok := s.tweets[tweetID]
	if !ok {
		return "", domain.ErrVideoNotFound
	}

	if tweet.Author.LocalAvatarURL != "" {
		return tweet.Author.LocalAvatarURL, nil
	}

	// Fallback: try avatar.jpg in archive path
	avatarPath := filepath.Join(tweet.ArchivePath, "avatar.jpg")
	if _, err := os.Stat(avatarPath); err == nil {
		return avatarPath, nil
	}

	return "", domain.ErrMediaNotFound
}

func getMediaType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return "image"
	case ".mp4", ".webm", ".mov":
		return "video"
	default:
		return "unknown"
	}
}

func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}
