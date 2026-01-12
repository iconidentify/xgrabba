package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/service"
)

// TweetHandler handles tweet archival HTTP requests.
type TweetHandler struct {
	tweetSvc *service.TweetService
	logger   *slog.Logger
}

// NewTweetHandler creates a new tweet handler.
func NewTweetHandler(tweetSvc *service.TweetService, logger *slog.Logger) *TweetHandler {
	return &TweetHandler{
		tweetSvc: tweetSvc,
		logger:   logger,
	}
}

// ArchiveRequest is the JSON request body for tweet archival.
// The extension only needs to send the tweet URL - backend handles everything.
type ArchiveRequest struct {
	TweetURL string `json:"tweet_url"`
}

// ArchiveResponse is the JSON response after submission.
type ArchiveResponse struct {
	TweetID string `json:"tweet_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// TweetResponse represents a tweet in list/get responses.
type TweetResponse struct {
	TweetID           string         `json:"tweet_id"`
	URL               string         `json:"url"`
	Status            string         `json:"status"`
	Author            string         `json:"author,omitempty"`
	AuthorDisplayName string         `json:"author_display_name,omitempty"`
	AuthorAvatar      string         `json:"author_avatar,omitempty"`
	Verified          bool           `json:"verified,omitempty"`
	FollowerCount     int            `json:"follower_count,omitempty"`
	FollowingCount    int            `json:"following_count,omitempty"`
	TweetCount        int            `json:"tweet_count,omitempty"`
	AuthorBio         string         `json:"author_bio,omitempty"`
	Text              string         `json:"text,omitempty"`
	MediaCount        int            `json:"media_count"`
	Media             []MediaPreview `json:"media,omitempty"`
	// Tweet engagement metrics
	LikeCount    int `json:"like_count,omitempty"`
	RetweetCount int `json:"retweet_count,omitempty"`
	ReplyCount   int `json:"reply_count,omitempty"`
	QuoteCount   int `json:"quote_count,omitempty"`
	ViewCount    int `json:"view_count,omitempty"`
	// AI metadata
	AITitle       string   `json:"ai_title,omitempty"`
	AISummary     string   `json:"ai_summary,omitempty"`
	AITags        []string `json:"ai_tags,omitempty"`
	AIContentType string   `json:"ai_content_type,omitempty"`
	AITopics      []string `json:"ai_topics,omitempty"`
	// True while Regenerate AI (and other AI backfills) are running
	AIInProgress bool `json:"ai_in_progress,omitempty"`
	// Video transcripts (combined from all video media)
	Transcripts []string `json:"transcripts,omitempty"`
	// Aggregated per-media AI (for search)
	MediaTags     []string  `json:"media_tags,omitempty"`
	MediaCaptions []string  `json:"media_captions,omitempty"`
	ArchivePath   string    `json:"archive_path,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// MediaPreview represents a media item in list responses for thumbnails.
type MediaPreview struct {
	Type         string `json:"type"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	URL          string `json:"url,omitempty"`
}

// TweetListResponse contains paginated tweet list.
type TweetListResponse struct {
	Tweets []TweetResponse `json:"tweets"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// Archive handles POST /api/v1/tweets
// This is the main endpoint - just send tweet URL and we handle everything.
func (h *TweetHandler) Archive(w http.ResponseWriter, r *http.Request) {
	var req ArchiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TweetURL == "" {
		h.writeError(w, http.StatusBadRequest, "tweet_url is required")
		return
	}

	h.logger.Info("archive request received", "url", req.TweetURL)

	result, err := h.tweetSvc.Archive(r.Context(), service.ArchiveRequest{
		TweetURL: req.TweetURL,
	})

	if err != nil {
		if errors.Is(err, domain.ErrInvalidTweetURL) {
			h.writeError(w, http.StatusBadRequest, "invalid tweet URL - must be a valid x.com or twitter.com URL")
			return
		}
		h.logger.Error("archive failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to archive tweet")
		return
	}

	h.writeJSON(w, http.StatusAccepted, ArchiveResponse{
		TweetID: string(result.TweetID),
		Status:  string(result.Status),
		Message: result.Message,
	})
}

// List handles GET /api/v1/tweets
func (h *TweetHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	tweets, total, err := h.tweetSvc.List(r.Context(), limit, offset)
	if err != nil {
		h.logger.Error("list failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to list tweets")
		return
	}

	response := TweetListResponse{
		Tweets: make([]TweetResponse, 0, len(tweets)),
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}

	for _, t := range tweets {
		// Build media previews with API URLs - all media served from our server
		mediaPreviews := make([]MediaPreview, 0, len(t.Media))
		for _, m := range t.Media {
			if m.LocalPath == "" {
				continue // Skip media not yet downloaded
			}
			filename := filepath.Base(m.LocalPath)
			mediaURL := fmt.Sprintf("/api/v1/tweets/%s/media/%s", t.ID, filename)

			mp := MediaPreview{
				Type: string(m.Type),
				URL:  mediaURL,
			}
			// For videos, use locally downloaded thumbnail; for images, use the image itself
			if m.Type == domain.MediaTypeVideo || m.Type == domain.MediaTypeGIF {
				// PreviewURL now contains local path after download
				if m.PreviewURL != "" && filepath.IsAbs(m.PreviewURL) {
					thumbFilename := filepath.Base(m.PreviewURL)
					mp.ThumbnailURL = fmt.Sprintf("/api/v1/tweets/%s/media/%s", t.ID, thumbFilename)
				} else {
					// Fallback: no thumbnail, use empty (UI will show placeholder)
					mp.ThumbnailURL = ""
				}
			} else {
				mp.ThumbnailURL = mediaURL // For images, use our served URL
			}
			mediaPreviews = append(mediaPreviews, mp)
		}

		// Use local avatar URL if available, otherwise construct API URL
		avatarURL := t.Author.AvatarURL
		if t.Author.LocalAvatarURL != "" {
			avatarURL = fmt.Sprintf("/api/v1/tweets/%s/avatar", t.ID)
		}

		// Collect transcripts from video media
		var transcripts []string
		// Collect per-media AI for search
		var mediaTags []string
		var mediaCaptions []string
		for _, m := range t.Media {
			if m.Transcript != "" {
				transcripts = append(transcripts, m.Transcript)
			}
			if len(m.AITags) > 0 {
				mediaTags = append(mediaTags, m.AITags...)
			}
			if m.AICaption != "" {
				mediaCaptions = append(mediaCaptions, m.AICaption)
			}
		}

		tr := TweetResponse{
			TweetID:           string(t.ID),
			URL:               t.URL,
			Status:            string(t.Status),
			Author:            t.Author.Username,
			AuthorDisplayName: t.Author.DisplayName,
			AuthorAvatar:      avatarURL,
			Verified:          t.Author.Verified,
			FollowerCount:     t.Author.FollowerCount,
			FollowingCount:    t.Author.FollowingCount,
			TweetCount:        t.Author.TweetCount,
			AuthorBio:         t.Author.Description,
			Text:              truncateText(t.Text, 200),
			MediaCount:        len(t.Media),
			Media:             mediaPreviews,
			LikeCount:         t.Metrics.Likes,
			RetweetCount:      t.Metrics.Retweets,
			ReplyCount:        t.Metrics.Replies,
			QuoteCount:        t.Metrics.Quotes,
			ViewCount:         t.Metrics.Views,
			AITitle:           t.AITitle,
			AISummary:         t.AISummary,
			AITags:            t.AITags,
			AIContentType:     t.AIContentType,
			AITopics:          t.AITopics,
			AIInProgress:      h.tweetSvc.IsAIAnalysisInProgress(t.ID),
			Transcripts:       transcripts,
			MediaTags:         mediaTags,
			MediaCaptions:     mediaCaptions,
			ArchivePath:       t.ArchivePath,
			Error:             t.Error,
			CreatedAt:         t.CreatedAt,
		}
		response.Tweets = append(response.Tweets, tr)
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Get handles GET /api/v1/tweets/{tweetID}
func (h *TweetHandler) Get(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	status, err := h.tweetSvc.GetStatus(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("get failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get tweet")
		return
	}

	h.writeJSON(w, http.StatusOK, TweetResponse{
		TweetID:     string(status.TweetID),
		URL:         "",
		Status:      string(status.Status),
		Author:      status.Author,
		Text:        status.Text,
		MediaCount:  status.MediaCount,
		AITitle:     status.AITitle,
		ArchivePath: status.ArchivePath,
		Error:       status.Error,
		CreatedAt:   status.CreatedAt,
	})
}

// GetStatus handles GET /api/v1/tweets/{tweetID}/status
func (h *TweetHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	h.Get(w, r)
}

// Delete handles DELETE /api/v1/tweets/{tweetID}
func (h *TweetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	err := h.tweetSvc.Delete(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("delete failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to delete tweet")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RegenerateAI handles POST /api/v1/tweets/{tweetID}/regenerate-ai
// This re-runs AI analysis on a tweet using the latest algorithm (including vision).
func (h *TweetHandler) RegenerateAI(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	// Run in background so closing the UI / disconnecting the client does not cancel the work.
	err := h.tweetSvc.StartRegenerateAIMetadata(domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		// Check if already processing
		if errors.Is(err, service.ErrAIAlreadyInProgress) || strings.Contains(err.Error(), "already in progress") {
			h.writeJSON(w, http.StatusConflict, map[string]interface{}{
				"success":     false,
				"message":     "AI analysis already in progress for this tweet",
				"in_progress": true,
			})
			return
		}
		h.logger.Error("regenerate AI failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to regenerate AI metadata")
		return
	}

	// 202 Accepted: work is running asynchronously
	h.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"success":     true,
		"message":     "AI regeneration started",
		"in_progress": true,
	})
}

// CheckAIAnalysisStatus handles GET /api/v1/tweets/{tweetID}/ai-status
// Returns whether AI analysis is currently in progress for a tweet.
func (h *TweetHandler) CheckAIAnalysisStatus(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	inProgress := h.tweetSvc.IsAIAnalysisInProgress(domain.TweetID(tweetID))
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"in_progress": inProgress,
	})
}

type TweetDiagnosticsResponse struct {
	TweetID string `json:"tweet_id"`

	AIInProgress bool `json:"ai_in_progress"`

	Pipeline service.PipelineDiagnostics `json:"pipeline"`

	ArchivePath string `json:"archive_path,omitempty"`

	HasVideo  bool `json:"has_video"`
	HasImages bool `json:"has_images"`

	Media []struct {
		ID               string `json:"id"`
		Type             string `json:"type"`
		LocalPath        string `json:"local_path,omitempty"`
		HasThumbnail     bool   `json:"has_thumbnail"`
		ThumbnailPath    string `json:"thumbnail_path,omitempty"`
		KeyframesDir     string `json:"keyframes_dir,omitempty"`
		KeyframesCount   int    `json:"keyframes_count"`
		TranscriptLength int    `json:"transcript_length"`
		TranscriptLang   string `json:"transcript_language,omitempty"`
	} `json:"media"`
}

// GetDiagnostics handles GET /api/v1/tweets/{tweetID}/diagnostics
func (h *TweetHandler) GetDiagnostics(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	archivePath, err := h.tweetSvc.GetArchivePath(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("get diagnostics failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get diagnostics")
		return
	}

	stored, err := h.tweetSvc.GetFullTweet(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("get full tweet failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get tweet")
		return
	}

	resp := TweetDiagnosticsResponse{
		TweetID:      tweetID,
		AIInProgress: h.tweetSvc.IsAIAnalysisInProgress(domain.TweetID(tweetID)),
		Pipeline:     h.tweetSvc.GetPipelineDiagnostics(),
		ArchivePath:  archivePath,
		HasVideo:     false,
		HasImages:    false,
		Media: make([]struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			LocalPath        string `json:"local_path,omitempty"`
			HasThumbnail     bool   `json:"has_thumbnail"`
			ThumbnailPath    string `json:"thumbnail_path,omitempty"`
			KeyframesDir     string `json:"keyframes_dir,omitempty"`
			KeyframesCount   int    `json:"keyframes_count"`
			TranscriptLength int    `json:"transcript_length"`
			TranscriptLang   string `json:"transcript_language,omitempty"`
		}, 0, len(stored.Media)),
	}

	for _, m := range stored.Media {
		mt := string(m.Type)
		if mt == "video" || mt == "gif" {
			resp.HasVideo = true
		}
		if mt == "image" {
			resp.HasImages = true
		}

		item := struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			LocalPath        string `json:"local_path,omitempty"`
			HasThumbnail     bool   `json:"has_thumbnail"`
			ThumbnailPath    string `json:"thumbnail_path,omitempty"`
			KeyframesDir     string `json:"keyframes_dir,omitempty"`
			KeyframesCount   int    `json:"keyframes_count"`
			TranscriptLength int    `json:"transcript_length"`
			TranscriptLang   string `json:"transcript_language,omitempty"`
		}{
			ID:               m.ID,
			Type:             mt,
			LocalPath:        m.LocalPath,
			TranscriptLength: len(m.Transcript),
			TranscriptLang:   m.TranscriptLanguage,
		}

		// Thumbnail
		if (mt == "video" || mt == "gif") && m.PreviewURL != "" && filepath.IsAbs(m.PreviewURL) {
			item.HasThumbnail = true
			item.ThumbnailPath = m.PreviewURL
		}

		// Keyframes directory and count
		if mt == "video" || mt == "gif" {
			kd := filepath.Join(archivePath, "media", "keyframes_"+m.ID)
			item.KeyframesDir = kd
			if entries, err := os.ReadDir(kd); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					ext := strings.ToLower(filepath.Ext(e.Name()))
					if ext == ".jpg" || ext == ".jpeg" {
						item.KeyframesCount++
					}
				}
			}
		}

		resp.Media = append(resp.Media, item)
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// MediaFileResponse represents a media file in the list response.
type MediaFileResponse struct {
	Filename           string   `json:"filename"`
	Type               string   `json:"type"`
	Size               int64    `json:"size"`
	URL                string   `json:"url"`
	ContentType        string   `json:"content_type"`
	Width              int      `json:"width,omitempty"`
	Height             int      `json:"height,omitempty"`
	Duration           int      `json:"duration_seconds,omitempty"`
	Transcript         string   `json:"transcript,omitempty"`          // Video transcript
	TranscriptLanguage string   `json:"transcript_language,omitempty"` // Detected language
	AICaption          string   `json:"ai_caption,omitempty"`
	AITags             []string `json:"ai_tags,omitempty"`
	AIContentType      string   `json:"ai_content_type,omitempty"`
	AITopics           []string `json:"ai_topics,omitempty"`
}

// MediaListResponse contains the list of media files.
type MediaListResponse struct {
	TweetID string              `json:"tweet_id"`
	Files   []MediaFileResponse `json:"files"`
}

// FullTweetResponse contains complete tweet details with media URLs.
type FullTweetResponse struct {
	TweetID       string              `json:"tweet_id"`
	URL           string              `json:"url"`
	Author        domain.Author       `json:"author"`
	Text          string              `json:"text"`
	PostedAt      time.Time           `json:"posted_at"`
	ArchivedAt    time.Time           `json:"archived_at"`
	Media         []MediaFileResponse `json:"media"`
	Metrics       domain.TweetMetrics `json:"metrics"`
	ReplyTo       string              `json:"reply_to,omitempty"`
	QuotedTweet   string              `json:"quoted_tweet,omitempty"`
	AITitle       string              `json:"ai_title"`
	AISummary     string              `json:"ai_summary,omitempty"`
	AITags        []string            `json:"ai_tags,omitempty"`
	AIContentType string              `json:"ai_content_type,omitempty"`
	AITopics      []string            `json:"ai_topics,omitempty"`
}

// ListMedia handles GET /api/v1/tweets/{tweetID}/media
func (h *TweetHandler) ListMedia(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	files, err := h.tweetSvc.ListMediaFiles(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("list media failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to list media")
		return
	}

	response := MediaListResponse{
		TweetID: tweetID,
		Files:   make([]MediaFileResponse, 0, len(files)),
	}

	for _, f := range files {
		response.Files = append(response.Files, MediaFileResponse{
			Filename:    f.Filename,
			Type:        f.Type,
			Size:        f.Size,
			URL:         fmt.Sprintf("/api/v1/tweets/%s/media/%s", tweetID, f.Filename),
			ContentType: f.ContentType,
		})
	}

	h.writeJSON(w, http.StatusOK, response)
}

// ServeMedia handles GET /api/v1/tweets/{tweetID}/media/{filename}
func (h *TweetHandler) ServeMedia(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	filename := chi.URLParam(r, "filename")

	if tweetID == "" || filename == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID or filename")
		return
	}

	// Security: validate filename to prevent path traversal
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		h.writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	filePath, err := h.tweetSvc.GetMediaFilePath(r.Context(), domain.TweetID(tweetID), filename)
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		if errors.Is(err, domain.ErrMediaNotFound) {
			h.writeError(w, http.StatusNotFound, "media file not found")
			return
		}
		h.logger.Error("get media file failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get media")
		return
	}

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "media file not found")
		return
	}
	defer file.Close()

	// Get file info for size and modtime
	stat, err := file.Stat()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to stat file")
		return
	}

	// Determine content type
	contentType := getContentTypeFromFilename(filename)
	w.Header().Set("Content-Type", contentType)

	// http.ServeContent handles Range requests automatically
	http.ServeContent(w, r, filename, stat.ModTime(), file)
}

// ServeAvatar handles GET /api/v1/tweets/{tweetID}/avatar
func (h *TweetHandler) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	filePath, err := h.tweetSvc.GetAvatarPath(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.writeError(w, http.StatusNotFound, "avatar not found")
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "avatar not found")
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to stat file")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, "avatar.jpg", stat.ModTime(), file)
}

// GetFull handles GET /api/v1/tweets/{tweetID}/full
func (h *TweetHandler) GetFull(w http.ResponseWriter, r *http.Request) {
	tweetID := chi.URLParam(r, "tweetID")
	if tweetID == "" {
		h.writeError(w, http.StatusBadRequest, "missing tweet ID")
		return
	}

	stored, err := h.tweetSvc.GetFullTweet(r.Context(), domain.TweetID(tweetID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "tweet not found")
			return
		}
		h.logger.Error("get full tweet failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get tweet")
		return
	}

	// Build media responses with API URLs and file sizes
	mediaResponses := make([]MediaFileResponse, 0, len(stored.Media))
	for _, m := range stored.Media {
		filename := filepath.Base(m.LocalPath)
		if filename == "" || filename == "." {
			continue
		}

		// Get file size
		var size int64
		if info, err := os.Stat(m.LocalPath); err == nil {
			size = info.Size()
		}

		mediaResp := MediaFileResponse{
			Filename:      filename,
			Type:          string(m.Type),
			URL:           fmt.Sprintf("/api/v1/tweets/%s/media/%s", tweetID, filename),
			ContentType:   getContentTypeFromMediaType(m.Type),
			Size:          size,
			Width:         m.Width,
			Height:        m.Height,
			Duration:      m.Duration,
			AICaption:     m.AICaption,
			AITags:        m.AITags,
			AIContentType: m.AIContentType,
			AITopics:      m.AITopics,
		}
		// Include transcript for videos
		if m.Transcript != "" {
			mediaResp.Transcript = m.Transcript
			mediaResp.TranscriptLanguage = m.TranscriptLanguage
		}
		mediaResponses = append(mediaResponses, mediaResp)
	}

	// Use local avatar URL if available
	author := stored.Author
	if stored.Author.LocalAvatarURL != "" {
		author.AvatarURL = fmt.Sprintf("/api/v1/tweets/%s/avatar", tweetID)
	}

	response := FullTweetResponse{
		TweetID:       stored.TweetID,
		URL:           stored.URL,
		Author:        author,
		Text:          stored.Text,
		PostedAt:      stored.PostedAt,
		ArchivedAt:    stored.ArchivedAt,
		Media:         mediaResponses,
		Metrics:       stored.Metrics,
		ReplyTo:       stored.ReplyTo,
		QuotedTweet:   stored.QuotedTweet,
		AITitle:       stored.AITitle,
		AISummary:     stored.AISummary,
		AITags:        stored.AITags,
		AIContentType: stored.AIContentType,
		AITopics:      stored.AITopics,
	}

	h.writeJSON(w, http.StatusOK, response)
}

func getContentTypeFromFilename(filename string) string {
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

func getContentTypeFromMediaType(mediaType domain.MediaType) string {
	switch mediaType {
	case domain.MediaTypeImage:
		return "image/jpeg"
	case domain.MediaTypeVideo, domain.MediaTypeGIF:
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func (h *TweetHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *TweetHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
