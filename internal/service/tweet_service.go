package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/downloader"
	"github.com/iconidentify/xgrabba/pkg/grok"
	"github.com/iconidentify/xgrabba/pkg/twitter"
)

// TweetService orchestrates tweet archiving workflow.
type TweetService struct {
	twitterClient *twitter.Client
	grokClient    grok.Client
	downloader    *downloader.HTTPDownloader
	cfg           config.StorageConfig
	logger        *slog.Logger

	// In-memory storage (could be replaced with DB)
	tweets map[domain.TweetID]*domain.Tweet
}

// NewTweetService creates a new tweet service.
func NewTweetService(
	grokClient grok.Client,
	dl *downloader.HTTPDownloader,
	storageCfg config.StorageConfig,
	logger *slog.Logger,
) *TweetService {
	return &TweetService{
		twitterClient: twitter.NewClient(),
		grokClient:    grokClient,
		downloader:    dl,
		cfg:           storageCfg,
		logger:        logger,
		tweets:        make(map[domain.TweetID]*domain.Tweet),
	}
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

	return title, ""
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

	return nil
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

// List returns archived tweets.
func (s *TweetService) List(ctx context.Context, limit, offset int) ([]*domain.Tweet, int, error) {
	var result []*domain.Tweet
	for _, tweet := range s.tweets {
		result = append(result, tweet)
	}

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
