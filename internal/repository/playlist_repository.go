package repository

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// PlaylistStore is the JSON structure for persisting playlists.
type PlaylistStore struct {
	Playlists []domain.Playlist `json:"playlists"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// FilesystemPlaylistRepository implements playlist storage using the filesystem.
type FilesystemPlaylistRepository struct {
	basePath  string
	storePath string
	mu        sync.RWMutex
	playlists map[domain.PlaylistID]*domain.Playlist
}

// NewFilesystemPlaylistRepository creates a new filesystem-based playlist repository.
func NewFilesystemPlaylistRepository(basePath string) *FilesystemPlaylistRepository {
	repo := &FilesystemPlaylistRepository{
		basePath:  basePath,
		storePath: filepath.Join(basePath, "playlists.json"),
		playlists: make(map[domain.PlaylistID]*domain.Playlist),
	}
	repo.load()
	return repo
}

// load reads playlists from disk into memory.
func (r *FilesystemPlaylistRepository) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No playlists yet
		}
		return err
	}

	var store PlaylistStore
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	for i := range store.Playlists {
		p := store.Playlists[i]
		r.playlists[p.ID] = &p
	}

	return nil
}

// save writes all playlists to disk.
func (r *FilesystemPlaylistRepository) save() error {
	playlists := make([]domain.Playlist, 0, len(r.playlists))
	for _, p := range r.playlists {
		playlists = append(playlists, *p)
	}

	store := PlaylistStore{
		Playlists: playlists,
		UpdatedAt: time.Now(),
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(r.basePath, 0755); err != nil {
		return err
	}

	// Write atomically via temp file
	tempPath := r.storePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tempPath, r.storePath)
}

// Create adds a new playlist.
func (r *FilesystemPlaylistRepository) Create(ctx context.Context, playlist *domain.Playlist) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate name
	for _, p := range r.playlists {
		if p.Name == playlist.Name {
			return domain.ErrDuplicatePlaylist
		}
	}

	r.playlists[playlist.ID] = playlist
	return r.save()
}

// Get retrieves a playlist by ID.
func (r *FilesystemPlaylistRepository) Get(ctx context.Context, id domain.PlaylistID) (*domain.Playlist, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	playlist, ok := r.playlists[id]
	if !ok {
		return nil, domain.ErrPlaylistNotFound
	}

	// Return a copy to prevent modification
	copy := *playlist
	copy.Items = make([]string, len(playlist.Items))
	copy.Items = append(copy.Items[:0], playlist.Items...)
	return &copy, nil
}

// List returns all playlists.
func (r *FilesystemPlaylistRepository) List(ctx context.Context) ([]*domain.Playlist, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*domain.Playlist, 0, len(r.playlists))
	for _, p := range r.playlists {
		copy := *p
		copy.Items = make([]string, len(p.Items))
		copy.Items = append(copy.Items[:0], p.Items...)
		result = append(result, &copy)
	}

	// Sort by name
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Name > result[j].Name {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result, nil
}

// Update modifies an existing playlist.
func (r *FilesystemPlaylistRepository) Update(ctx context.Context, playlist *domain.Playlist) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.playlists[playlist.ID]; !ok {
		return domain.ErrPlaylistNotFound
	}

	// Check for duplicate name (excluding current playlist)
	for _, p := range r.playlists {
		if p.ID != playlist.ID && p.Name == playlist.Name {
			return domain.ErrDuplicatePlaylist
		}
	}

	r.playlists[playlist.ID] = playlist
	return r.save()
}

// Delete removes a playlist.
func (r *FilesystemPlaylistRepository) Delete(ctx context.Context, id domain.PlaylistID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.playlists[id]; !ok {
		return domain.ErrPlaylistNotFound
	}

	delete(r.playlists, id)
	return r.save()
}

// GetAll returns all playlists as a slice for export purposes.
func (r *FilesystemPlaylistRepository) GetAll(ctx context.Context) ([]domain.Playlist, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]domain.Playlist, 0, len(r.playlists))
	for _, p := range r.playlists {
		result = append(result, *p)
	}

	return result, nil
}
