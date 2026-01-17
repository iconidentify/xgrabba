package handler

import (
	"context"
	"io"
	"log/slog"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/repository"
)

// testLogger returns a silent logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockJobRepository is a test implementation of repository.JobRepository.
type mockJobRepository struct {
	stats       *repository.QueueStats
	statsErr    error
	jobs        map[domain.JobID]*domain.Job
	enqueueErr  error
	dequeueErr  error
}

func newMockJobRepository() *mockJobRepository {
	return &mockJobRepository{
		stats: &repository.QueueStats{},
		jobs:  make(map[domain.JobID]*domain.Job),
	}
}

func (m *mockJobRepository) Enqueue(ctx context.Context, job *domain.Job) error {
	if m.enqueueErr != nil {
		return m.enqueueErr
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *mockJobRepository) Dequeue(ctx context.Context) (*domain.Job, error) {
	if m.dequeueErr != nil {
		return nil, m.dequeueErr
	}
	for _, job := range m.jobs {
		if job.Status == domain.JobStatusQueued {
			return job, nil
		}
	}
	return nil, domain.ErrNoJobs
}

func (m *mockJobRepository) Get(ctx context.Context, id domain.JobID) (*domain.Job, error) {
	if job, ok := m.jobs[id]; ok {
		return job, nil
	}
	return nil, domain.ErrJobNotFound
}

func (m *mockJobRepository) Update(ctx context.Context, job *domain.Job) error {
	m.jobs[job.ID] = job
	return nil
}

func (m *mockJobRepository) Stats(ctx context.Context) (*repository.QueueStats, error) {
	if m.statsErr != nil {
		return nil, m.statsErr
	}
	return m.stats, nil
}

func (m *mockJobRepository) GetByVideoID(ctx context.Context, videoID domain.VideoID) (*domain.Job, error) {
	for _, job := range m.jobs {
		if job.VideoID == videoID {
			return job, nil
		}
	}
	return nil, domain.ErrJobNotFound
}

func (m *mockJobRepository) ListPending(ctx context.Context) ([]*domain.Job, error) {
	var pending []*domain.Job
	for _, job := range m.jobs {
		if job.Status == domain.JobStatusQueued || job.Status == domain.JobStatusRetrying {
			pending = append(pending, job)
		}
	}
	return pending, nil
}

// mockPlaylistService is a test implementation for PlaylistHandler tests.
type mockPlaylistService struct {
	playlists      map[domain.PlaylistID]*domain.Playlist
	listErr        error
	createErr      error
	getErr         error
	updateErr      error
	deleteErr      error
	addItemErr     error
	removeItemErr  error
	reorderErr     error
	addMultipleErr error
}

func newMockPlaylistService() *mockPlaylistService {
	return &mockPlaylistService{
		playlists: make(map[domain.PlaylistID]*domain.Playlist),
	}
}

func (m *mockPlaylistService) List(ctx context.Context) ([]*domain.Playlist, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]*domain.Playlist, 0, len(m.playlists))
	for _, p := range m.playlists {
		result = append(result, p)
	}
	return result, nil
}

func (m *mockPlaylistService) Create(ctx context.Context, name, description string) (*domain.Playlist, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if name == "" {
		return nil, domain.ErrEmptyPlaylistName
	}
	// Check for duplicates
	for _, p := range m.playlists {
		if p.Name == name {
			return nil, domain.ErrDuplicatePlaylist
		}
	}
	p := &domain.Playlist{
		ID:          domain.PlaylistID("pl-" + name),
		Name:        name,
		Description: description,
		Items:       []string{},
	}
	m.playlists[p.ID] = p
	return p, nil
}

func (m *mockPlaylistService) Get(ctx context.Context, id domain.PlaylistID) (*domain.Playlist, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	p, ok := m.playlists[id]
	if !ok {
		return nil, domain.ErrPlaylistNotFound
	}
	return p, nil
}

func (m *mockPlaylistService) Update(ctx context.Context, id domain.PlaylistID, name, description string) (*domain.Playlist, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	if name == "" {
		return nil, domain.ErrEmptyPlaylistName
	}
	p, ok := m.playlists[id]
	if !ok {
		return nil, domain.ErrPlaylistNotFound
	}
	// Check for duplicates (excluding self)
	for _, other := range m.playlists {
		if other.ID != id && other.Name == name {
			return nil, domain.ErrDuplicatePlaylist
		}
	}
	p.Name = name
	p.Description = description
	return p, nil
}

func (m *mockPlaylistService) Delete(ctx context.Context, id domain.PlaylistID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.playlists[id]; !ok {
		return domain.ErrPlaylistNotFound
	}
	delete(m.playlists, id)
	return nil
}

func (m *mockPlaylistService) AddItem(ctx context.Context, id domain.PlaylistID, tweetID string) error {
	if m.addItemErr != nil {
		return m.addItemErr
	}
	p, ok := m.playlists[id]
	if !ok {
		return domain.ErrPlaylistNotFound
	}
	p.Items = append(p.Items, tweetID)
	return nil
}

func (m *mockPlaylistService) RemoveItem(ctx context.Context, id domain.PlaylistID, tweetID string) error {
	if m.removeItemErr != nil {
		return m.removeItemErr
	}
	p, ok := m.playlists[id]
	if !ok {
		return domain.ErrPlaylistNotFound
	}
	for i, item := range p.Items {
		if item == tweetID {
			p.Items = append(p.Items[:i], p.Items[i+1:]...)
			return nil
		}
	}
	return domain.ErrTweetNotInPlaylist
}

func (m *mockPlaylistService) Reorder(ctx context.Context, id domain.PlaylistID, newOrder []string) error {
	if m.reorderErr != nil {
		return m.reorderErr
	}
	p, ok := m.playlists[id]
	if !ok {
		return domain.ErrPlaylistNotFound
	}
	p.Items = newOrder
	return nil
}

func (m *mockPlaylistService) AddToMultiple(ctx context.Context, ids []domain.PlaylistID, tweetID string) error {
	if m.addMultipleErr != nil {
		return m.addMultipleErr
	}
	for _, id := range ids {
		p, ok := m.playlists[id]
		if !ok {
			return domain.ErrPlaylistNotFound
		}
		p.Items = append(p.Items, tweetID)
	}
	return nil
}
