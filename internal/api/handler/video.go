package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/service"
)

// VideoHandler handles video-related HTTP requests.
type VideoHandler struct {
	videoSvc *service.VideoService
	logger   *slog.Logger
}

// NewVideoHandler creates a new video handler.
func NewVideoHandler(videoSvc *service.VideoService, logger *slog.Logger) *VideoHandler {
	return &VideoHandler{
		videoSvc: videoSvc,
		logger:   logger,
	}
}

// SubmitRequest is the JSON request body for video submission.
type SubmitRequest struct {
	TweetURL  string          `json:"tweet_url"`
	TweetID   string          `json:"tweet_id,omitempty"`
	MediaURLs []string        `json:"media_urls"`
	Metadata  MetadataRequest `json:"metadata"`
}

// MetadataRequest contains tweet metadata from the extension.
type MetadataRequest struct {
	AuthorUsername string `json:"author_username"`
	AuthorName     string `json:"author_name"`
	TweetText      string `json:"tweet_text"`
	PostedAt       string `json:"posted_at"`
	Duration       int    `json:"duration_seconds,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
}

// SubmitResponse is the JSON response after submission.
type SubmitResponse struct {
	VideoID string `json:"video_id"`
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// VideoResponse represents a video in list/get responses.
type VideoResponse struct {
	VideoID     string    `json:"video_id"`
	TweetURL    string    `json:"tweet_url"`
	TweetID     string    `json:"tweet_id"`
	Status      string    `json:"status"`
	Filename    string    `json:"filename,omitempty"`
	FilePath    string    `json:"file_path,omitempty"`
	Author      string    `json:"author"`
	TweetText   string    `json:"tweet_text"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ProcessedAt time.Time `json:"processed_at,omitempty"`
}

// StatusResponse is returned for status queries.
type StatusResponse struct {
	VideoID  string `json:"video_id"`
	Status   string `json:"status"`
	Progress string `json:"progress"`
	Filename string `json:"filename,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ListResponse contains paginated video list.
type ListResponse struct {
	Videos []VideoResponse `json:"videos"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// Submit handles POST /api/v1/videos
func (h *VideoHandler) Submit(w http.ResponseWriter, r *http.Request) {
	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Parse posted_at timestamp
	var postedAt time.Time
	if req.Metadata.PostedAt != "" {
		var err error
		postedAt, err = time.Parse(time.RFC3339, req.Metadata.PostedAt)
		if err != nil {
			// Try alternate formats
			postedAt, err = time.Parse("2006-01-02T15:04:05Z", req.Metadata.PostedAt)
			if err != nil {
				postedAt = time.Now()
			}
		}
	} else {
		postedAt = time.Now()
	}

	// Submit to service
	result, err := h.videoSvc.Submit(r.Context(), service.SubmitRequest{
		TweetURL:  req.TweetURL,
		TweetID:   req.TweetID,
		MediaURLs: req.MediaURLs,
		Metadata: domain.VideoMetadata{
			AuthorUsername: req.Metadata.AuthorUsername,
			AuthorName:     req.Metadata.AuthorName,
			TweetText:      req.Metadata.TweetText,
			PostedAt:       postedAt,
			Duration:       req.Metadata.Duration,
			Resolution:     req.Metadata.Resolution,
		},
	})

	if err != nil {
		if errors.Is(err, domain.ErrInvalidTweetURL) {
			h.writeError(w, http.StatusBadRequest, "invalid tweet URL")
			return
		}
		if errors.Is(err, domain.ErrNoMediaURLs) {
			h.writeError(w, http.StatusBadRequest, "no media URLs provided")
			return
		}
		h.logger.Error("submit failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to submit video")
		return
	}

	h.writeJSON(w, http.StatusAccepted, SubmitResponse{
		VideoID: string(result.VideoID),
		JobID:   string(result.JobID),
		Status:  string(result.Status),
		Message: result.Message,
	})
}

// List handles GET /api/v1/videos
func (h *VideoHandler) List(w http.ResponseWriter, r *http.Request) {
	// Parse query params
	limit := 50
	offset := 0
	var status *domain.VideoStatus

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

	if s := r.URL.Query().Get("status"); s != "" {
		st := domain.VideoStatus(s)
		status = &st
	}

	videos, total, err := h.videoSvc.List(r.Context(), status, limit, offset)
	if err != nil {
		h.logger.Error("list failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to list videos")
		return
	}

	response := ListResponse{
		Videos: make([]VideoResponse, 0, len(videos)),
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}

	for _, v := range videos {
		vr := VideoResponse{
			VideoID:   string(v.ID),
			TweetURL:  v.TweetURL,
			TweetID:   v.TweetID,
			Status:    string(v.Status),
			Filename:  v.Filename,
			FilePath:  v.FilePath,
			Author:    v.Metadata.AuthorUsername,
			TweetText: v.Metadata.TweetText,
			Error:     v.Error,
			CreatedAt: v.CreatedAt,
		}
		if v.ProcessedAt != nil {
			vr.ProcessedAt = *v.ProcessedAt
		}
		response.Videos = append(response.Videos, vr)
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Get handles GET /api/v1/videos/{videoID}
func (h *VideoHandler) Get(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	if videoID == "" {
		h.writeError(w, http.StatusBadRequest, "missing video ID")
		return
	}

	status, err := h.videoSvc.GetStatus(r.Context(), domain.VideoID(videoID))
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			h.writeError(w, http.StatusNotFound, "video not found")
			return
		}
		h.logger.Error("get failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to get video")
		return
	}

	h.writeJSON(w, http.StatusOK, StatusResponse{
		VideoID:  string(status.VideoID),
		Status:   string(status.Status),
		Progress: status.Progress,
		Filename: status.Filename,
		FilePath: status.FilePath,
		Error:    status.Error,
	})
}

// GetStatus handles GET /api/v1/videos/{videoID}/status
func (h *VideoHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	h.Get(w, r) // Same implementation as Get
}

func (h *VideoHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *VideoHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
