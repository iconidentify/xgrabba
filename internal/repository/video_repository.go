package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
)

// FilesystemVideoRepository implements VideoRepository using the filesystem.
type FilesystemVideoRepository struct {
	basePath string
	tempPath string
	mu       sync.RWMutex
	videos   map[domain.VideoID]*domain.Video
	byTweet  map[string]domain.VideoID
}

// NewFilesystemVideoRepository creates a new filesystem-based video repository.
func NewFilesystemVideoRepository(cfg config.StorageConfig) *FilesystemVideoRepository {
	return &FilesystemVideoRepository{
		basePath: cfg.BasePath,
		tempPath: cfg.TempPath,
		videos:   make(map[domain.VideoID]*domain.Video),
		byTweet:  make(map[string]domain.VideoID),
	}
}

// Save persists video bytes and metadata to filesystem.
func (r *FilesystemVideoRepository) Save(ctx context.Context, video *domain.Video, content io.Reader) error {
	// Ensure directory exists
	dir := filepath.Dir(video.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Write to temp file first, then rename for atomicity
	tempFile := filepath.Join(r.tempPath, fmt.Sprintf("%s.tmp", video.ID))
	if err := os.MkdirAll(r.tempPath, 0755); err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}

	f, err := os.Create(tempFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	_, err = io.Copy(f, content)
	f.Close()
	if err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("write video: %w", err)
	}

	// Move to final location
	if err := os.Rename(tempFile, video.FilePath); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("move video to final location: %w", err)
	}

	// Save metadata
	if err := r.SaveMetadata(ctx, video); err != nil {
		return err
	}

	// Update in-memory index
	r.mu.Lock()
	r.videos[video.ID] = video
	if video.TweetID != "" {
		r.byTweet[video.TweetID] = video.ID
	}
	r.mu.Unlock()

	return nil
}

// SaveMetadata writes the .json sidecar file.
func (r *FilesystemVideoRepository) SaveMetadata(ctx context.Context, video *domain.Video) error {
	metadata := video.ToStoredMetadata()

	jsonPath := strings.TrimSuffix(video.FilePath, filepath.Ext(video.FilePath)) + ".json"

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// Get retrieves video metadata by ID.
func (r *FilesystemVideoRepository) Get(ctx context.Context, id domain.VideoID) (*domain.Video, error) {
	r.mu.RLock()
	video, ok := r.videos[id]
	r.mu.RUnlock()

	if !ok {
		return nil, domain.ErrVideoNotFound
	}

	return video, nil
}

// GetByTweetID retrieves video by tweet ID.
func (r *FilesystemVideoRepository) GetByTweetID(ctx context.Context, tweetID string) (*domain.Video, error) {
	r.mu.RLock()
	videoID, ok := r.byTweet[tweetID]
	r.mu.RUnlock()

	if !ok {
		return nil, domain.ErrVideoNotFound
	}

	return r.Get(ctx, videoID)
}

// List returns all videos, optionally filtered by status.
func (r *FilesystemVideoRepository) List(ctx context.Context, status *domain.VideoStatus, limit, offset int) ([]*domain.Video, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*domain.Video
	for _, video := range r.videos {
		if status != nil && video.Status != *status {
			continue
		}
		result = append(result, video)
	}

	// Sort by creation time (newest first)
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].CreatedAt.Before(result[j].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	// Apply pagination
	if offset >= len(result) {
		return []*domain.Video{}, nil
	}
	result = result[offset:]
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Count returns the total number of videos.
func (r *FilesystemVideoRepository) Count(ctx context.Context, status *domain.VideoStatus) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if status == nil {
		return len(r.videos), nil
	}

	count := 0
	for _, video := range r.videos {
		if video.Status == *status {
			count++
		}
	}
	return count, nil
}

// UpdateStatus changes video status.
func (r *FilesystemVideoRepository) UpdateStatus(ctx context.Context, id domain.VideoID, status domain.VideoStatus, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	video, ok := r.videos[id]
	if !ok {
		return domain.ErrVideoNotFound
	}

	video.Status = status
	video.Error = errMsg

	if status == domain.StatusCompleted {
		now := time.Now()
		video.ProcessedAt = &now
	}

	return nil
}

// Delete removes a video and its metadata.
func (r *FilesystemVideoRepository) Delete(ctx context.Context, id domain.VideoID) error {
	r.mu.Lock()
	video, ok := r.videos[id]
	if !ok {
		r.mu.Unlock()
		return domain.ErrVideoNotFound
	}

	delete(r.videos, id)
	if video.TweetID != "" {
		delete(r.byTweet, video.TweetID)
	}
	r.mu.Unlock()

	// Remove files
	if video.FilePath != "" {
		os.Remove(video.FilePath)
		jsonPath := strings.TrimSuffix(video.FilePath, filepath.Ext(video.FilePath)) + ".json"
		os.Remove(jsonPath)
	}

	return nil
}

// Register adds a video to the in-memory index without saving to disk.
// Used when creating a new video entry before download.
func (r *FilesystemVideoRepository) Register(video *domain.Video) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.videos[video.ID] = video
	if video.TweetID != "" {
		r.byTweet[video.TweetID] = video.ID
	}
}

// BuildStoragePath creates the file path for a video based on date organization.
func (r *FilesystemVideoRepository) BuildStoragePath(video *domain.Video) string {
	year := video.Metadata.PostedAt.Format("2006")
	month := video.Metadata.PostedAt.Format("01")
	return filepath.Join(r.basePath, year, month, video.Filename)
}
