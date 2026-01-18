package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/repository"
)

// PlaylistService handles playlist business logic.
type PlaylistService struct {
	repo     *repository.FilesystemPlaylistRepository
	tweetSvc *TweetService
	logger   *slog.Logger
}

// NewPlaylistService creates a new playlist service.
func NewPlaylistService(
	repo *repository.FilesystemPlaylistRepository,
	tweetSvc *TweetService,
	logger *slog.Logger,
) *PlaylistService {
	return &PlaylistService{
		repo:     repo,
		tweetSvc: tweetSvc,
		logger:   logger,
	}
}

// generateID creates a simple ID from timestamp and random suffix.
func generatePlaylistID() domain.PlaylistID {
	// Use timestamp-based ID for simplicity
	return domain.PlaylistID(time.Now().Format("20060102150405"))
}

// Create creates a new manual playlist.
func (s *PlaylistService) Create(ctx context.Context, name, description string) (*domain.Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.ErrEmptyPlaylistName
	}

	now := time.Now()
	playlist := &domain.Playlist{
		ID:          generatePlaylistID(),
		Name:        name,
		Description: strings.TrimSpace(description),
		Type:        domain.PlaylistTypeManual,
		Items:       []string{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.Create(ctx, playlist); err != nil {
		return nil, err
	}

	s.logger.Info("created playlist", "id", playlist.ID, "name", playlist.Name, "type", playlist.Type)
	return playlist, nil
}

// CreateSmart creates a new smart playlist with a search query.
func (s *PlaylistService) CreateSmart(ctx context.Context, name, description, query string, limit int) (*domain.Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.ErrEmptyPlaylistName
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, domain.ErrEmptySmartQuery
	}

	if limit <= 0 {
		limit = 1000 // Default limit - allow large playlists
	}

	now := time.Now()
	playlist := &domain.Playlist{
		ID:          generatePlaylistID(),
		Name:        name,
		Description: strings.TrimSpace(description),
		Type:        domain.PlaylistTypeSmart,
		SmartConfig: &domain.SmartPlaylistConfig{
			Query: query,
			Limit: limit,
		},
		Items:     []string{}, // Will be populated dynamically
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.Create(ctx, playlist); err != nil {
		return nil, err
	}

	// Populate items immediately for the response
	if err := s.populateSmartPlaylistItems(ctx, playlist); err != nil {
		s.logger.Warn("failed to populate smart playlist items", "id", playlist.ID, "error", err)
	}

	s.logger.Info("created smart playlist", "id", playlist.ID, "name", playlist.Name, "query", query)
	return playlist, nil
}

// Get retrieves a playlist by ID.
func (s *PlaylistService) Get(ctx context.Context, id domain.PlaylistID) (*domain.Playlist, error) {
	playlist, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// For smart playlists, populate items dynamically
	if playlist.IsSmart() {
		if err := s.populateSmartPlaylistItems(ctx, playlist); err != nil {
			s.logger.Warn("failed to populate smart playlist items", "id", id, "error", err)
		}
	}

	return playlist, nil
}

// List returns all playlists.
func (s *PlaylistService) List(ctx context.Context) ([]*domain.Playlist, error) {
	return s.repo.List(ctx)
}

// Update modifies a playlist's name and description.
func (s *PlaylistService) Update(ctx context.Context, id domain.PlaylistID, name, description string) (*domain.Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.ErrEmptyPlaylistName
	}

	playlist, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	playlist.Name = name
	playlist.Description = strings.TrimSpace(description)
	playlist.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, playlist); err != nil {
		return nil, err
	}

	s.logger.Info("updated playlist", "id", playlist.ID, "name", playlist.Name)
	return playlist, nil
}

// Delete removes a playlist.
func (s *PlaylistService) Delete(ctx context.Context, id domain.PlaylistID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.logger.Info("deleted playlist", "id", id)
	return nil
}

// AddItem adds a tweet to a playlist.
func (s *PlaylistService) AddItem(ctx context.Context, playlistID domain.PlaylistID, tweetID string) error {
	playlist, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}

	// Smart playlists cannot have items manually added
	if playlist.IsSmart() {
		return domain.ErrSmartPlaylistNoManualItems
	}

	if !playlist.AddItem(tweetID) {
		// Item already in playlist, not an error but log it
		s.logger.Debug("tweet already in playlist", "playlist_id", playlistID, "tweet_id", tweetID)
		return nil
	}

	if err := s.repo.Update(ctx, playlist); err != nil {
		return err
	}

	s.logger.Info("added item to playlist", "playlist_id", playlistID, "tweet_id", tweetID)
	return nil
}

// RemoveItem removes a tweet from a playlist.
func (s *PlaylistService) RemoveItem(ctx context.Context, playlistID domain.PlaylistID, tweetID string) error {
	playlist, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}

	// Smart playlists cannot have items manually removed
	if playlist.IsSmart() {
		return domain.ErrSmartPlaylistNoManualItems
	}

	if !playlist.RemoveItem(tweetID) {
		return domain.ErrTweetNotInPlaylist
	}

	if err := s.repo.Update(ctx, playlist); err != nil {
		return err
	}

	s.logger.Info("removed item from playlist", "playlist_id", playlistID, "tweet_id", tweetID)
	return nil
}

// Reorder updates the order of items in a playlist.
func (s *PlaylistService) Reorder(ctx context.Context, playlistID domain.PlaylistID, newOrder []string) error {
	playlist, err := s.repo.Get(ctx, playlistID)
	if err != nil {
		return err
	}

	// Smart playlists cannot be reordered
	if playlist.IsSmart() {
		return domain.ErrSmartPlaylistNoReorder
	}

	// Validate that newOrder contains the same items
	if len(newOrder) != len(playlist.Items) {
		return domain.ErrTweetNotInPlaylist
	}

	existingItems := make(map[string]bool)
	for _, id := range playlist.Items {
		existingItems[id] = true
	}

	for _, id := range newOrder {
		if !existingItems[id] {
			return domain.ErrTweetNotInPlaylist
		}
	}

	playlist.Reorder(newOrder)

	if err := s.repo.Update(ctx, playlist); err != nil {
		return err
	}

	s.logger.Info("reordered playlist", "playlist_id", playlistID)
	return nil
}

// GetAll returns all playlists for export purposes.
func (s *PlaylistService) GetAll(ctx context.Context) ([]domain.Playlist, error) {
	return s.repo.GetAll(ctx)
}

// AddToMultiple adds a tweet to multiple playlists at once.
func (s *PlaylistService) AddToMultiple(ctx context.Context, playlistIDs []domain.PlaylistID, tweetID string) error {
	for _, playlistID := range playlistIDs {
		if err := s.AddItem(ctx, playlistID, tweetID); err != nil {
			return err
		}
	}
	return nil
}

// Preview returns matching items for a search query without creating a playlist.
// This is used for the modal preview when creating a smart playlist.
func (s *PlaylistService) Preview(ctx context.Context, query string, limit int) ([]*domain.Tweet, int, error) {
	if s.tweetSvc == nil {
		return nil, 0, nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return []*domain.Tweet{}, 0, nil
	}

	if limit <= 0 {
		limit = 20 // Default preview limit
	}

	tweets, total, err := s.tweetSvc.Search(ctx, query, limit, 0)
	if err != nil {
		return nil, 0, err
	}

	return tweets, total, nil
}

// populateSmartPlaylistItems executes the search query and fills the Items field.
func (s *PlaylistService) populateSmartPlaylistItems(ctx context.Context, playlist *domain.Playlist) error {
	if !playlist.IsSmart() || playlist.SmartConfig == nil {
		return nil
	}

	if s.tweetSvc == nil {
		return nil
	}

	limit := playlist.SmartConfig.Limit
	if limit <= 0 {
		limit = 1000 // Default limit - allow large playlists
	}

	tweets, _, err := s.tweetSvc.Search(ctx, playlist.SmartConfig.Query, limit, 0)
	if err != nil {
		return err
	}

	// Extract tweet IDs - only include tweets with media (video or image)
	playlist.Items = make([]string, 0, len(tweets))
	for _, tweet := range tweets {
		if len(tweet.Media) > 0 {
			playlist.Items = append(playlist.Items, string(tweet.ID))
		}
	}

	return nil
}
