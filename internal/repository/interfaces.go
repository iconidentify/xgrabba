package repository

import (
	"context"
	"io"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// VideoRepository handles video persistence.
type VideoRepository interface {
	// Save persists video bytes and metadata to filesystem.
	Save(ctx context.Context, video *domain.Video, content io.Reader) error

	// SaveMetadata writes the .json sidecar file.
	SaveMetadata(ctx context.Context, video *domain.Video) error

	// Get retrieves video metadata by ID.
	Get(ctx context.Context, id domain.VideoID) (*domain.Video, error)

	// GetByTweetID retrieves video by tweet ID.
	GetByTweetID(ctx context.Context, tweetID string) (*domain.Video, error)

	// List returns all videos, optionally filtered by status.
	List(ctx context.Context, status *domain.VideoStatus, limit, offset int) ([]*domain.Video, error)

	// Count returns the total number of videos.
	Count(ctx context.Context, status *domain.VideoStatus) (int, error)

	// UpdateStatus changes video status.
	UpdateStatus(ctx context.Context, id domain.VideoID, status domain.VideoStatus, errMsg string) error

	// Delete removes a video and its metadata.
	Delete(ctx context.Context, id domain.VideoID) error
}

// JobRepository manages the job queue.
type JobRepository interface {
	// Enqueue adds a job to the queue.
	Enqueue(ctx context.Context, job *domain.Job) error

	// Dequeue retrieves the next pending job (FIFO).
	Dequeue(ctx context.Context) (*domain.Job, error)

	// Update modifies job state.
	Update(ctx context.Context, job *domain.Job) error

	// Get retrieves a job by ID.
	Get(ctx context.Context, id domain.JobID) (*domain.Job, error)

	// GetByVideoID finds job associated with a video.
	GetByVideoID(ctx context.Context, videoID domain.VideoID) (*domain.Job, error)

	// ListPending returns all pending/retrying jobs.
	ListPending(ctx context.Context) ([]*domain.Job, error)

	// Stats returns queue statistics.
	Stats(ctx context.Context) (*QueueStats, error)
}

// QueueStats contains job queue statistics.
type QueueStats struct {
	Queued     int
	Processing int
	Completed  int
	Failed     int
	Retrying   int
}
