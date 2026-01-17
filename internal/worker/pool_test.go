package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/repository"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockJobRepository implements repository.JobRepository for testing.
type mockJobRepository struct {
	mu           sync.Mutex
	jobs         []*domain.Job
	dequeueErr   error
	updateErr    error
	dequeueCalls int
	updateCalls  int
}

func (m *mockJobRepository) Enqueue(ctx context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, job)
	return nil
}

func (m *mockJobRepository) Get(ctx context.Context, id domain.JobID) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, domain.ErrJobNotFound
}

func (m *mockJobRepository) GetByVideoID(ctx context.Context, videoID domain.VideoID) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.VideoID == videoID {
			return j, nil
		}
	}
	return nil, domain.ErrJobNotFound
}

func (m *mockJobRepository) Update(ctx context.Context, job *domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	for i, j := range m.jobs {
		if j.ID == job.ID {
			m.jobs[i] = job
			return nil
		}
	}
	return nil
}

func (m *mockJobRepository) Dequeue(ctx context.Context) (*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dequeueCalls++
	if m.dequeueErr != nil {
		return nil, m.dequeueErr
	}
	for _, j := range m.jobs {
		if j.Status == domain.JobStatusQueued {
			return j, nil
		}
	}
	return nil, domain.ErrNoJobs
}

func (m *mockJobRepository) ListPending(ctx context.Context) ([]*domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pending []*domain.Job
	for _, j := range m.jobs {
		if j.Status == domain.JobStatusQueued {
			pending = append(pending, j)
		}
	}
	return pending, nil
}

func (m *mockJobRepository) Stats(ctx context.Context) (*repository.QueueStats, error) {
	return &repository.QueueStats{}, nil
}

func TestNewPool(t *testing.T) {
	repo := &mockJobRepository{}
	logger := testLogger()

	cfg := Config{
		Workers:      3,
		PollInterval: 10 * time.Second,
	}

	pool := NewPool(cfg, repo, nil, logger)

	if pool == nil {
		t.Fatal("pool should not be nil")
	}
	if pool.workers != 3 {
		t.Errorf("workers = %d, want 3", pool.workers)
	}
	if pool.pollInterval != 10*time.Second {
		t.Errorf("pollInterval = %v, want 10s", pool.pollInterval)
	}
}

func TestNewPool_DefaultValues(t *testing.T) {
	repo := &mockJobRepository{}
	logger := testLogger()

	// Zero values should use defaults
	cfg := Config{
		Workers:      0,
		PollInterval: 0,
	}

	pool := NewPool(cfg, repo, nil, logger)

	if pool.workers != 2 {
		t.Errorf("default workers = %d, want 2", pool.workers)
	}
	if pool.pollInterval != 5*time.Second {
		t.Errorf("default pollInterval = %v, want 5s", pool.pollInterval)
	}
}

func TestNewPool_NegativeValues(t *testing.T) {
	repo := &mockJobRepository{}
	logger := testLogger()

	cfg := Config{
		Workers:      -1,
		PollInterval: -1 * time.Second,
	}

	pool := NewPool(cfg, repo, nil, logger)

	if pool.workers != 2 {
		t.Errorf("negative workers should default to 2, got %d", pool.workers)
	}
	if pool.pollInterval != 5*time.Second {
		t.Errorf("negative pollInterval should default to 5s, got %v", pool.pollInterval)
	}
}

func TestPool_StartStop(t *testing.T) {
	repo := &mockJobRepository{
		dequeueErr: domain.ErrNoJobs,
	}
	logger := testLogger()

	pool := NewPool(Config{
		Workers:      2,
		PollInterval: 50 * time.Millisecond,
	}, repo, nil, logger)

	pool.Start()

	// Let workers run a bit
	time.Sleep(100 * time.Millisecond)

	err := pool.Stop(2 * time.Second)
	if err != nil {
		t.Errorf("Stop should not error: %v", err)
	}
}

func TestPool_StopTimeout(t *testing.T) {
	repo := &mockJobRepository{
		dequeueErr: domain.ErrNoJobs,
	}
	logger := testLogger()

	pool := NewPool(Config{
		Workers:      1,
		PollInterval: 10 * time.Second, // Long poll interval
	}, repo, nil, logger)

	// Override the pool's cancel to simulate workers that don't respond
	oldCancel := pool.cancel
	pool.cancel = func() {
		// Don't call the real cancel, simulating stuck workers
	}

	// Add a fake worker count that will never decrement
	pool.wg.Add(1)

	err := pool.Stop(50 * time.Millisecond)

	// Cleanup: call real cancel and done
	oldCancel()
	pool.wg.Done()

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Errorf("expected ErrShutdownTimeout, got %v", err)
	}
}

func TestPool_DequeueError(t *testing.T) {
	expectedErr := errors.New("database connection error")
	repo := &mockJobRepository{
		dequeueErr: expectedErr,
	}
	logger := testLogger()

	pool := NewPool(Config{
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
	}, repo, nil, logger)

	pool.Start()

	// Let workers attempt dequeue
	time.Sleep(50 * time.Millisecond)

	err := pool.Stop(1 * time.Second)
	if err != nil {
		t.Errorf("Stop should succeed: %v", err)
	}

	// Should have attempted dequeue
	if repo.dequeueCalls == 0 {
		t.Error("expected at least one dequeue call")
	}
}

func TestPool_ProcessJob_UpdateError(t *testing.T) {
	job := &domain.Job{
		ID:         "job-1",
		VideoID:    "video-1",
		Status:     domain.JobStatusQueued,
		MaxRetries: 3,
	}

	repo := &mockJobRepository{
		jobs:      []*domain.Job{job},
		updateErr: errors.New("update failed"),
	}
	logger := testLogger()

	pool := NewPool(Config{
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
	}, repo, nil, logger)

	pool.Start()

	// Let worker try to process
	time.Sleep(50 * time.Millisecond)

	pool.Stop(1 * time.Second)

	// Should have attempted to dequeue and update
	if repo.dequeueCalls == 0 {
		t.Error("expected dequeue calls")
	}
	if repo.updateCalls == 0 {
		t.Error("expected update calls")
	}
}

func TestConfig(t *testing.T) {
	cfg := Config{
		Workers:      5,
		PollInterval: 30 * time.Second,
	}

	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5", cfg.Workers)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want 30s", cfg.PollInterval)
	}
}

func TestErrShutdownTimeout(t *testing.T) {
	if ErrShutdownTimeout.Error() != "worker pool shutdown timed out" {
		t.Errorf("unexpected error message: %s", ErrShutdownTimeout.Error())
	}
}
