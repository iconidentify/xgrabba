package repository

import (
	"context"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

func TestNewInMemoryJobRepository(t *testing.T) {
	repo := NewInMemoryJobRepository()

	if repo == nil {
		t.Fatal("repo should not be nil")
	}
	if repo.jobs == nil {
		t.Error("jobs map should be initialized")
	}
	if repo.byVideo == nil {
		t.Error("byVideo map should be initialized")
	}
	if repo.queue == nil {
		t.Error("queue should be initialized")
	}
}

func TestInMemoryJobRepository_Enqueue(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)

	err := repo.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Verify job is stored
	retrieved, err := repo.Get(ctx, "job-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.ID != "job-1" {
		t.Errorf("ID = %q, want %q", retrieved.ID, "job-1")
	}
}

func TestInMemoryJobRepository_Dequeue(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Empty queue
	_, err := repo.Dequeue(ctx)
	if err != domain.ErrNoJobs {
		t.Errorf("expected ErrNoJobs, got %v", err)
	}

	// Add jobs
	job1 := domain.NewJob("job-1", "video-1", 3)
	job2 := domain.NewJob("job-2", "video-2", 3)
	repo.Enqueue(ctx, job1)
	repo.Enqueue(ctx, job2)

	// Dequeue should return first job (FIFO)
	dequeued, err := repo.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if dequeued.ID != "job-1" {
		t.Errorf("expected job-1, got %s", dequeued.ID)
	}

	// Dequeue again should return second job
	dequeued, err = repo.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if dequeued.ID != "job-2" {
		t.Errorf("expected job-2, got %s", dequeued.ID)
	}

	// Queue should be empty now
	_, err = repo.Dequeue(ctx)
	if err != domain.ErrNoJobs {
		t.Errorf("expected ErrNoJobs, got %v", err)
	}
}

func TestInMemoryJobRepository_Dequeue_SkipsNonPending(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Create a completed job
	job1 := domain.NewJob("job-1", "video-1", 3)
	job1.Status = domain.JobStatusCompleted
	repo.Enqueue(ctx, job1)

	// Create a queued job
	job2 := domain.NewJob("job-2", "video-2", 3)
	repo.Enqueue(ctx, job2)

	// Should dequeue job-2 (skips completed job)
	dequeued, err := repo.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if dequeued.ID != "job-2" {
		t.Errorf("expected job-2, got %s", dequeued.ID)
	}
}

func TestInMemoryJobRepository_Dequeue_RetryingJobs(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)
	job.Status = domain.JobStatusRetrying
	repo.Enqueue(ctx, job)

	// Should dequeue retrying job
	dequeued, err := repo.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if dequeued.ID != "job-1" {
		t.Errorf("expected job-1, got %s", dequeued.ID)
	}
}

func TestInMemoryJobRepository_Update(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)
	repo.Enqueue(ctx, job)

	// Update the job
	job.Status = domain.JobStatusProcessing
	err := repo.Update(ctx, job)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	retrieved, _ := repo.Get(ctx, "job-1")
	if retrieved.Status != domain.JobStatusProcessing {
		t.Errorf("Status = %v, want %v", retrieved.Status, domain.JobStatusProcessing)
	}
}

func TestInMemoryJobRepository_Update_NotFound(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("nonexistent", "video-1", 3)
	err := repo.Update(ctx, job)
	if err != domain.ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
}

func TestInMemoryJobRepository_Update_RequeueRetrying(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)
	repo.Enqueue(ctx, job)

	// Dequeue the job
	repo.Dequeue(ctx)

	// Mark as retrying
	job.Status = domain.JobStatusRetrying
	repo.Update(ctx, job)

	// Should be able to dequeue again
	dequeued, err := repo.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if dequeued.ID != "job-1" {
		t.Errorf("expected job-1, got %s", dequeued.ID)
	}
}

func TestInMemoryJobRepository_Get(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)
	repo.Enqueue(ctx, job)

	retrieved, err := repo.Get(ctx, "job-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.VideoID != "video-1" {
		t.Errorf("VideoID = %q, want %q", retrieved.VideoID, "video-1")
	}
}

func TestInMemoryJobRepository_Get_NotFound(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	_, err := repo.Get(ctx, "nonexistent")
	if err != domain.ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
}

func TestInMemoryJobRepository_GetByVideoID(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	job := domain.NewJob("job-1", "video-1", 3)
	repo.Enqueue(ctx, job)

	retrieved, err := repo.GetByVideoID(ctx, "video-1")
	if err != nil {
		t.Fatalf("GetByVideoID failed: %v", err)
	}
	if retrieved.ID != "job-1" {
		t.Errorf("ID = %q, want %q", retrieved.ID, "job-1")
	}
}

func TestInMemoryJobRepository_GetByVideoID_NotFound(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	_, err := repo.GetByVideoID(ctx, "nonexistent")
	if err != domain.ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
}

func TestInMemoryJobRepository_ListPending(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Empty initially
	pending, err := repo.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending failed: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected empty list, got %d items", len(pending))
	}

	// Add jobs with various statuses
	job1 := domain.NewJob("job-1", "video-1", 3)
	job1.Status = domain.JobStatusQueued
	repo.Enqueue(ctx, job1)

	job2 := domain.NewJob("job-2", "video-2", 3)
	job2.Status = domain.JobStatusProcessing
	repo.Enqueue(ctx, job2)

	job3 := domain.NewJob("job-3", "video-3", 3)
	job3.Status = domain.JobStatusRetrying
	repo.Enqueue(ctx, job3)

	job4 := domain.NewJob("job-4", "video-4", 3)
	job4.Status = domain.JobStatusCompleted
	repo.Enqueue(ctx, job4)

	pending, _ = repo.ListPending(ctx)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending jobs, got %d", len(pending))
	}
}

func TestInMemoryJobRepository_Stats(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Empty stats
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.Queued != 0 || stats.Processing != 0 || stats.Completed != 0 || stats.Failed != 0 || stats.Retrying != 0 {
		t.Error("expected all zeros for empty repo")
	}

	// Add jobs with various statuses
	statuses := []domain.JobStatus{
		domain.JobStatusQueued,
		domain.JobStatusQueued,
		domain.JobStatusProcessing,
		domain.JobStatusCompleted,
		domain.JobStatusCompleted,
		domain.JobStatusCompleted,
		domain.JobStatusFailed,
		domain.JobStatusRetrying,
	}

	for i, status := range statuses {
		job := domain.NewJob(domain.JobID(string(rune('a'+i))), domain.VideoID(string(rune('a'+i))), 3)
		job.Status = status
		repo.Enqueue(ctx, job)
	}

	stats, _ = repo.Stats(ctx)
	if stats.Queued != 2 {
		t.Errorf("Queued = %d, want 2", stats.Queued)
	}
	if stats.Processing != 1 {
		t.Errorf("Processing = %d, want 1", stats.Processing)
	}
	if stats.Completed != 3 {
		t.Errorf("Completed = %d, want 3", stats.Completed)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
	if stats.Retrying != 1 {
		t.Errorf("Retrying = %d, want 1", stats.Retrying)
	}
}

func TestInMemoryJobRepository_Clear(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Add some jobs
	repo.Enqueue(ctx, domain.NewJob("job-1", "video-1", 3))
	repo.Enqueue(ctx, domain.NewJob("job-2", "video-2", 3))

	// Clear
	repo.Clear()

	// Should be empty
	_, err := repo.Get(ctx, "job-1")
	if err != domain.ErrJobNotFound {
		t.Error("expected job-1 to be cleared")
	}

	stats, _ := repo.Stats(ctx)
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued after clear, got %d", stats.Queued)
	}
}

func TestQueueStats(t *testing.T) {
	stats := &QueueStats{
		Queued:     5,
		Processing: 2,
		Completed:  10,
		Failed:     1,
		Retrying:   3,
	}

	if stats.Queued != 5 {
		t.Errorf("Queued = %d, want 5", stats.Queued)
	}
	if stats.Processing != 2 {
		t.Errorf("Processing = %d, want 2", stats.Processing)
	}
	if stats.Completed != 10 {
		t.Errorf("Completed = %d, want 10", stats.Completed)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
	if stats.Retrying != 3 {
		t.Errorf("Retrying = %d, want 3", stats.Retrying)
	}
}

func TestInMemoryJobRepository_Concurrency(t *testing.T) {
	repo := NewInMemoryJobRepository()
	ctx := context.Background()

	// Run concurrent operations
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(id int) {
			job := domain.NewJob(domain.JobID(time.Now().String()+string(rune(id))), domain.VideoID(string(rune(id))), 3)
			repo.Enqueue(ctx, job)
			repo.Stats(ctx)
			repo.ListPending(ctx)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
