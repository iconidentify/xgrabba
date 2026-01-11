package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/chrisk/xgrabba/internal/domain"
	"github.com/chrisk/xgrabba/internal/repository"
	"github.com/chrisk/xgrabba/internal/service"
)

// ErrShutdownTimeout is returned when workers don't stop within timeout.
var ErrShutdownTimeout = errors.New("worker pool shutdown timed out")

// Pool manages a pool of workers for processing video jobs.
type Pool struct {
	workers      int
	pollInterval time.Duration
	jobRepo      repository.JobRepository
	videoSvc     *service.VideoService
	logger       *slog.Logger

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// Config holds worker pool configuration.
type Config struct {
	Workers      int
	PollInterval time.Duration
}

// NewPool creates a new worker pool.
func NewPool(
	cfg Config,
	jobRepo repository.JobRepository,
	videoSvc *service.VideoService,
	logger *slog.Logger,
) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		workers:      cfg.Workers,
		pollInterval: cfg.PollInterval,
		jobRepo:      jobRepo,
		videoSvc:     videoSvc,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start launches all workers.
func (p *Pool) Start() {
	p.logger.Info("starting worker pool", "workers", p.workers)

	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Stop gracefully stops all workers.
func (p *Pool) Stop(timeout time.Duration) error {
	p.logger.Info("stopping worker pool")
	p.cancel()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("worker pool stopped gracefully")
		return nil
	case <-time.After(timeout):
		return ErrShutdownTimeout
	}
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()

	logger := p.logger.With("worker_id", id)
	logger.Info("worker started")

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			logger.Info("worker stopping")
			return
		case <-ticker.C:
			p.processNextJob(logger)
		}
	}
}

func (p *Pool) processNextJob(logger *slog.Logger) {
	job, err := p.jobRepo.Dequeue(p.ctx)
	if err != nil {
		if !errors.Is(err, domain.ErrNoJobs) {
			logger.Error("failed to dequeue job", "error", err)
		}
		return
	}

	logger = logger.With("job_id", job.ID, "video_id", job.VideoID)
	logger.Info("processing job")

	// Update job status to processing
	job.MarkProcessing()
	if err := p.jobRepo.Update(p.ctx, job); err != nil {
		logger.Error("failed to update job status", "error", err)
		return
	}

	// Process the video
	err = p.videoSvc.Process(p.ctx, job.VideoID)
	if err != nil {
		p.handleJobFailure(logger, job, err)
		return
	}

	// Mark completed
	job.MarkCompleted()
	if err := p.jobRepo.Update(p.ctx, job); err != nil {
		logger.Error("failed to mark job completed", "error", err)
	}

	logger.Info("job completed successfully")
}

func (p *Pool) handleJobFailure(logger *slog.Logger, job *domain.Job, err error) {
	job.MarkFailed(err.Error())

	if job.CanRetry() {
		logger.Warn("job failed, will retry",
			"error", err,
			"attempt", job.Attempts,
			"max_retries", job.MaxRetries,
		)
	} else {
		logger.Error("job failed permanently",
			"error", err,
			"attempts", job.Attempts,
		)

	}

	if updateErr := p.jobRepo.Update(p.ctx, job); updateErr != nil {
		logger.Error("failed to update job after failure", "error", updateErr)
	}
}
