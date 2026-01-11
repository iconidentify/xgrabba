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
	TweetID     string    `json:"tweet_id"`
	URL         string    `json:"url"`
	Status      string    `json:"status"`
	Author      string    `json:"author,omitempty"`
	Text        string    `json:"text,omitempty"`
	MediaCount  int       `json:"media_count"`
	AITitle     string    `json:"ai_title,omitempty"`
	ArchivePath string    `json:"archive_path,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
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
		tr := TweetResponse{
			TweetID:     string(t.ID),
			URL:         t.URL,
			Status:      string(t.Status),
			Author:      t.Author.Username,
			Text:        truncateText(t.Text, 200),
			MediaCount:  len(t.Media),
			AITitle:     t.AITitle,
			ArchivePath: t.ArchivePath,
			Error:       t.Error,
			CreatedAt:   t.CreatedAt,
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
