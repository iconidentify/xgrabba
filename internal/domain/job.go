package domain

import (
	"time"
)

// JobID is a unique identifier for a job.
type JobID string

// String returns the string representation of the JobID.
func (id JobID) String() string {
	return string(id)
}

// JobStatus represents the current state of a job.
type JobStatus string

const (
	JobStatusQueued     JobStatus = "queued"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusRetrying   JobStatus = "retrying"
)

// Job represents a video processing job in the queue.
type Job struct {
	ID         JobID
	VideoID    VideoID
	Status     JobStatus
	Attempts   int
	MaxRetries int
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewJob creates a new job for processing a video.
func NewJob(id JobID, videoID VideoID, maxRetries int) *Job {
	now := time.Now()
	return &Job{
		ID:         id,
		VideoID:    videoID,
		Status:     JobStatusQueued,
		Attempts:   0,
		MaxRetries: maxRetries,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// CanRetry returns true if the job can be retried.
func (j *Job) CanRetry() bool {
	return j.Attempts < j.MaxRetries
}

// MarkProcessing updates the job status to processing.
func (j *Job) MarkProcessing() {
	j.Status = JobStatusProcessing
	j.UpdatedAt = time.Now()
}

// MarkCompleted updates the job status to completed.
func (j *Job) MarkCompleted() {
	j.Status = JobStatusCompleted
	j.UpdatedAt = time.Now()
}

// MarkFailed updates the job status to failed with an error message.
func (j *Job) MarkFailed(err string) {
	j.Attempts++
	j.LastError = err
	j.UpdatedAt = time.Now()

	if j.CanRetry() {
		j.Status = JobStatusRetrying
	} else {
		j.Status = JobStatusFailed
	}
}

// MarkRetrying updates the job status to retrying.
func (j *Job) MarkRetrying() {
	j.Status = JobStatusRetrying
	j.UpdatedAt = time.Now()
}
