package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/service"
)

// PlaylistHandler handles playlist HTTP requests.
type PlaylistHandler struct {
	svc    *service.PlaylistService
	logger *slog.Logger
}

// NewPlaylistHandler creates a new playlist handler.
func NewPlaylistHandler(svc *service.PlaylistService, logger *slog.Logger) *PlaylistHandler {
	return &PlaylistHandler{
		svc:    svc,
		logger: logger,
	}
}

// CreateRequest is the JSON request body for playlist creation.
type CreatePlaylistRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CreateSmartPlaylistRequest is the JSON request body for smart playlist creation.
type CreateSmartPlaylistRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
}

// PlaylistResponse represents a playlist in API responses.
type PlaylistResponse struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Type        string                     `json:"type"`
	SmartConfig *domain.SmartPlaylistConfig `json:"smart_config,omitempty"`
	Items       []string                   `json:"items"`
	ItemCount   int                        `json:"item_count"`
	CreatedAt   string                     `json:"created_at"`
	UpdatedAt   string                     `json:"updated_at"`
}

// toResponse converts a domain playlist to API response.
func toPlaylistResponse(p *domain.Playlist) PlaylistResponse {
	playlistType := string(p.Type)
	if playlistType == "" {
		playlistType = string(domain.PlaylistTypeManual) // Default for legacy playlists
	}
	return PlaylistResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Description: p.Description,
		Type:        playlistType,
		SmartConfig: p.SmartConfig,
		Items:       p.Items,
		ItemCount:   len(p.Items),
		CreatedAt:   p.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   p.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// List returns all playlists.
func (h *PlaylistHandler) List(w http.ResponseWriter, r *http.Request) {
	playlists, err := h.svc.List(r.Context())
	if err != nil {
		h.logger.Error("failed to list playlists", "error", err)
		http.Error(w, "Failed to list playlists", http.StatusInternalServerError)
		return
	}

	response := make([]PlaylistResponse, 0, len(playlists))
	for _, p := range playlists {
		response = append(response, toPlaylistResponse(p))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Create creates a new playlist.
func (h *PlaylistHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	playlist, err := h.svc.Create(r.Context(), req.Name, req.Description)
	if err != nil {
		if errors.Is(err, domain.ErrEmptyPlaylistName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, domain.ErrDuplicatePlaylist) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.logger.Error("failed to create playlist", "error", err)
		http.Error(w, "Failed to create playlist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toPlaylistResponse(playlist))
}

// Get retrieves a single playlist.
func (h *PlaylistHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}

	playlist, err := h.svc.Get(r.Context(), domain.PlaylistID(id))
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		h.logger.Error("failed to get playlist", "id", id, "error", err)
		http.Error(w, "Failed to get playlist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPlaylistResponse(playlist))
}

// UpdateRequest is the JSON request body for playlist update.
type UpdatePlaylistRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Update modifies a playlist.
func (h *PlaylistHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}

	var req UpdatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	playlist, err := h.svc.Update(r.Context(), domain.PlaylistID(id), req.Name, req.Description)
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrEmptyPlaylistName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, domain.ErrDuplicatePlaylist) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.logger.Error("failed to update playlist", "id", id, "error", err)
		http.Error(w, "Failed to update playlist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPlaylistResponse(playlist))
}

// Delete removes a playlist.
func (h *PlaylistHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}

	err := h.svc.Delete(r.Context(), domain.PlaylistID(id))
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		h.logger.Error("failed to delete playlist", "id", id, "error", err)
		http.Error(w, "Failed to delete playlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AddItemRequest is the JSON request body for adding an item to a playlist.
type AddItemRequest struct {
	TweetID string `json:"tweet_id"`
}

// AddItem adds a tweet to a playlist.
func (h *PlaylistHandler) AddItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}

	var req AddItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TweetID == "" {
		http.Error(w, "Missing tweet_id", http.StatusBadRequest)
		return
	}

	err := h.svc.AddItem(r.Context(), domain.PlaylistID(id), req.TweetID)
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrSmartPlaylistNoManualItems) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.logger.Error("failed to add item to playlist", "id", id, "tweet_id", req.TweetID, "error", err)
		http.Error(w, "Failed to add item to playlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RemoveItem removes a tweet from a playlist.
func (h *PlaylistHandler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tweetID := chi.URLParam(r, "tweetId")

	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}
	if tweetID == "" {
		http.Error(w, "Missing tweet ID", http.StatusBadRequest)
		return
	}

	err := h.svc.RemoveItem(r.Context(), domain.PlaylistID(id), tweetID)
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrTweetNotInPlaylist) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrSmartPlaylistNoManualItems) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.logger.Error("failed to remove item from playlist", "id", id, "tweet_id", tweetID, "error", err)
		http.Error(w, "Failed to remove item from playlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReorderRequest is the JSON request body for reordering playlist items.
type ReorderRequest struct {
	Items []string `json:"items"`
}

// Reorder updates the order of items in a playlist.
func (h *PlaylistHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Missing playlist ID", http.StatusBadRequest)
		return
	}

	var req ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err := h.svc.Reorder(r.Context(), domain.PlaylistID(id), req.Items)
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrTweetNotInPlaylist) {
			http.Error(w, "Invalid item order: items don't match playlist contents", http.StatusBadRequest)
			return
		}
		if errors.Is(err, domain.ErrSmartPlaylistNoReorder) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.logger.Error("failed to reorder playlist", "id", id, "error", err)
		http.Error(w, "Failed to reorder playlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AddToMultipleRequest is for adding a tweet to multiple playlists at once.
type AddToMultipleRequest struct {
	PlaylistIDs []string `json:"playlist_ids"`
	TweetID     string   `json:"tweet_id"`
}

// AddToMultiple adds a tweet to multiple playlists.
func (h *PlaylistHandler) AddToMultiple(w http.ResponseWriter, r *http.Request) {
	var req AddToMultipleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TweetID == "" {
		http.Error(w, "Missing tweet_id", http.StatusBadRequest)
		return
	}
	if len(req.PlaylistIDs) == 0 {
		http.Error(w, "Missing playlist_ids", http.StatusBadRequest)
		return
	}

	playlistIDs := make([]domain.PlaylistID, len(req.PlaylistIDs))
	for i, id := range req.PlaylistIDs {
		playlistIDs[i] = domain.PlaylistID(id)
	}

	err := h.svc.AddToMultiple(r.Context(), playlistIDs, req.TweetID)
	if err != nil {
		if errors.Is(err, domain.ErrPlaylistNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrSmartPlaylistNoManualItems) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.logger.Error("failed to add to multiple playlists", "tweet_id", req.TweetID, "error", err)
		http.Error(w, "Failed to add to playlists", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CreateSmart creates a new smart playlist.
func (h *PlaylistHandler) CreateSmart(w http.ResponseWriter, r *http.Request) {
	var req CreateSmartPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	playlist, err := h.svc.CreateSmart(r.Context(), req.Name, req.Description, req.Query, req.Limit)
	if err != nil {
		if errors.Is(err, domain.ErrEmptyPlaylistName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, domain.ErrEmptySmartQuery) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, domain.ErrDuplicatePlaylist) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.logger.Error("failed to create smart playlist", "error", err)
		http.Error(w, "Failed to create smart playlist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toPlaylistResponse(playlist))
}

// PreviewResponse represents the preview results for a smart playlist query.
type PreviewResponse struct {
	Items []PreviewItem `json:"items"`
	Total int           `json:"total"`
}

// PreviewItem represents a single item in the preview results.
type PreviewItem struct {
	TweetID      string `json:"tweet_id"`
	AITitle      string `json:"ai_title,omitempty"`
	Text         string `json:"text,omitempty"`
	Author       string `json:"author,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

// Preview returns matching items for a search query without creating a playlist.
func (h *PlaylistHandler) Preview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PreviewResponse{Items: []PreviewItem{}, Total: 0})
		return
	}

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	tweets, _, err := h.svc.Preview(r.Context(), query, limit)
	if err != nil {
		h.logger.Error("failed to preview playlist", "query", query, "error", err)
		http.Error(w, "Failed to search", http.StatusInternalServerError)
		return
	}

	items := make([]PreviewItem, 0, len(tweets))
	mediaCount := 0
	for _, tweet := range tweets {
		// Skip tweets without media
		if len(tweet.Media) == 0 {
			continue
		}
		mediaCount++
		// Only include up to limit items in the response
		if len(items) >= limit {
			continue
		}
		item := PreviewItem{
			TweetID: string(tweet.ID),
			AITitle: tweet.AITitle,
			Text:    tweet.Text,
			Author:  tweet.Author.Username,
		}
		// Include first media thumbnail if available - use local filename for API path
		if tweet.Media[0].LocalPath != "" {
			item.ThumbnailURL = filepath.Base(tweet.Media[0].LocalPath)
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PreviewResponse{Items: items, Total: mediaCount})
}
