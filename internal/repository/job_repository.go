package repository

import (
	"context"
	"sync"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// InMemoryJobRepository implements JobRepository using in-memory storage.
type InMemoryJobRepository struct {
	mu      sync.RWMutex
	jobs    map[domain.JobID]*domain.Job
	byVideo map[domain.VideoID]domain.JobID
	queue   []domain.JobID // FIFO queue of pending job IDs
}

// NewInMemoryJobRepository creates a new in-memory job repository.
func NewInMemoryJobRepository() *InMemoryJobRepository {
	return &InMemoryJobRepository{
		jobs:    make(map[domain.JobID]*domain.Job),
		byVideo: make(map[domain.VideoID]domain.JobID),
		queue:   make([]domain.JobID, 0),
	}
}

// Enqueue adds a job to the queue.
func (r *InMemoryJobRepository) Enqueue(ctx context.Context, job *domain.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs[job.ID] = job
	r.byVideo[job.VideoID] = job.ID
	r.queue = append(r.queue, job.ID)

	return nil
}

// Dequeue retrieves the next pending job (FIFO).
func (r *InMemoryJobRepository) Dequeue(ctx context.Context) (*domain.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find first job that is queued or retrying
	for i, jobID := range r.queue {
		job, ok := r.jobs[jobID]
		if !ok {
			continue
		}

		if job.Status == domain.JobStatusQueued || job.Status == domain.JobStatusRetrying {
			// Remove from queue
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			return job, nil
		}
	}

	return nil, domain.ErrNoJobs
}

// Update modifies job state.
func (r *InMemoryJobRepository) Update(ctx context.Context, job *domain.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.jobs[job.ID]; !ok {
		return domain.ErrJobNotFound
	}

	r.jobs[job.ID] = job

	// If job is retrying, add back to queue
	if job.Status == domain.JobStatusRetrying {
		r.queue = append(r.queue, job.ID)
	}

	return nil
}

// Get retrieves a job by ID.
func (r *InMemoryJobRepository) Get(ctx context.Context, id domain.JobID) (*domain.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	job, ok := r.jobs[id]
	if !ok {
		return nil, domain.ErrJobNotFound
	}

	return job, nil
}

// GetByVideoID finds job associated with a video.
func (r *InMemoryJobRepository) GetByVideoID(ctx context.Context, videoID domain.VideoID) (*domain.Job, error) {
	r.mu.RLock()
	jobID, ok := r.byVideo[videoID]
	r.mu.RUnlock()

	if !ok {
		return nil, domain.ErrJobNotFound
	}

	return r.Get(ctx, jobID)
}

// ListPending returns all pending/retrying jobs.
func (r *InMemoryJobRepository) ListPending(ctx context.Context) ([]*domain.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*domain.Job
	for _, job := range r.jobs {
		if job.Status == domain.JobStatusQueued || job.Status == domain.JobStatusRetrying {
			result = append(result, job)
		}
	}

	return result, nil
}

// Stats returns queue statistics.
func (r *InMemoryJobRepository) Stats(ctx context.Context) (*QueueStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := &QueueStats{}
	for _, job := range r.jobs {
		switch job.Status {
		case domain.JobStatusQueued:
			stats.Queued++
		case domain.JobStatusProcessing:
			stats.Processing++
		case domain.JobStatusCompleted:
			stats.Completed++
		case domain.JobStatusFailed:
			stats.Failed++
		case domain.JobStatusRetrying:
			stats.Retrying++
		}
	}

	return stats, nil
}

// Clear removes all jobs (useful for testing).
func (r *InMemoryJobRepository) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs = make(map[domain.JobID]*domain.Job)
	r.byVideo = make(map[domain.VideoID]domain.JobID)
	r.queue = make([]domain.JobID, 0)
}
