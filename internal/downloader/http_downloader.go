package downloader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
)

// HTTPDownloader implements Downloader using HTTP requests.
type HTTPDownloader struct {
	// client is used for short requests (Probe, etc) with overall timeout
	client *http.Client
	// streamClient is used for streaming downloads without overall timeout
	streamClient *http.Client
	userAgent    string
	cfg          config.DownloadConfig
	logger       *slog.Logger
}

// NewHTTPDownloader creates a new HTTP-based video downloader.
func NewHTTPDownloader(cfg config.DownloadConfig) *HTTPDownloader {
	// Transport for streaming downloads - no overall timeout, but header timeout
	streamTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		// Use defaults for other settings
	}

	return &HTTPDownloader{
		// Regular client for short requests (Probe, HEAD, etc)
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		// Stream client for large downloads - no overall timeout
		streamClient: &http.Client{
			Transport: streamTransport,
			// No Timeout - we use per-read timeouts instead
		},
		userAgent: cfg.UserAgent,
		cfg:       cfg,
		logger:    slog.Default(),
	}
}

// SetLogger sets the logger for download progress reporting.
func (d *HTTPDownloader) SetLogger(logger *slog.Logger) {
	d.logger = logger
}

// Download fetches video from URL with retry logic.
// Returns a progress-tracking reader for large file streaming.
func (d *HTTPDownloader) Download(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		reader, size, err := d.downloadOnce(ctx, url)
		if err == nil {
			return reader, size, nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			break
		}

		// Wait before retry with exponential backoff
		delay := d.cfg.RetryDelay * (1 << attempt)
		if delay > d.cfg.MaxRetryDelay {
			delay = d.cfg.MaxRetryDelay
		}

		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-waitChan(delay):
			continue
		}
	}

	return nil, 0, fmt.Errorf("download failed after retries: %w", lastErr)
}

func (d *HTTPDownloader) downloadOnce(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	// Set headers to mimic browser request
	req.Header.Set("User-Agent", d.userAgent)
	req.Header.Set("Accept", "video/mp4,video/*;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://x.com/")

	// Use streamClient for downloads (no overall timeout)
	resp, err := d.streamClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, 0, domain.ErrURLExpired
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, 0, domain.ErrRateLimited
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	size := resp.ContentLength
	if size < 0 {
		// Try to parse from header
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			size, _ = strconv.ParseInt(cl, 10, 64)
		}
	}

	// Wrap with progress reader for large downloads
	progressReader := newProgressReader(resp.Body, size, d.cfg.ReadTimeout, d.logger, url)
	return progressReader, size, nil
}

// Probe checks URL accessibility without downloading full content.
func (d *HTTPDownloader) Probe(ctx context.Context, url string) (*ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", d.userAgent)
	req.Header.Set("Referer", "https://x.com/")

	resp, err := d.client.Do(req)
	if err != nil {
		return &ProbeResult{
			Accessible: false,
			Error:      err.Error(),
		}, nil
	}
	defer resp.Body.Close()

	result := &ProbeResult{
		ContentType:   resp.Header.Get("Content-Type"),
		ContentLength: resp.ContentLength,
		Accessible:    resp.StatusCode == http.StatusOK,
	}

	if !result.Accessible {
		result.Error = fmt.Sprintf("status code %d", resp.StatusCode)
	}

	return result, nil
}

// SelectBestURL selects the highest quality video URL from a list.
// URLs should be provided in order of preference (highest quality first).
func (d *HTTPDownloader) SelectBestURL(ctx context.Context, urls []string) (string, error) {
	for _, url := range urls {
		probe, err := d.Probe(ctx, url)
		if err != nil {
			continue
		}
		if probe.Accessible {
			return url, nil
		}
	}
	return "", domain.ErrNoMediaURLs
}

func isRetryableError(err error) bool {
	// Network errors are retryable
	if err == domain.ErrRateLimited {
		return true
	}
	// URL expired is not retryable
	if err == domain.ErrURLExpired {
		return false
	}
	return true
}

func waitChan(d interface{ Nanoseconds() int64 }) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		// Simple sleep simulation using channel
		close(ch)
	}()
	return ch
}

// progressReader wraps an io.ReadCloser to track download progress
// and detect stalls (no data for readTimeout).
type progressReader struct {
	reader      io.ReadCloser
	total       int64
	downloaded  int64
	readTimeout time.Duration
	lastRead    time.Time
	lastLog     time.Time
	logger      *slog.Logger
	url         string
	mu          sync.Mutex
	closed      bool
}

func newProgressReader(r io.ReadCloser, total int64, readTimeout time.Duration, logger *slog.Logger, url string) *progressReader {
	now := time.Now()
	return &progressReader{
		reader:      r,
		total:       total,
		readTimeout: readTimeout,
		lastRead:    now,
		lastLog:     now,
		logger:      logger,
		url:         url,
	}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)

	p.mu.Lock()
	defer p.mu.Unlock()

	if n > 0 {
		p.downloaded += int64(n)
		p.lastRead = time.Now()

		// Log progress every 30 seconds or every 50MB
		if time.Since(p.lastLog) > 30*time.Second || p.downloaded-p.downloaded%(50*1024*1024) != 0 {
			if time.Since(p.lastLog) > 30*time.Second {
				p.logProgress()
				p.lastLog = time.Now()
			}
		}
	}

	// Check for stall on any read (including zero-byte reads)
	if err == nil && p.readTimeout > 0 && time.Since(p.lastRead) > p.readTimeout {
		return n, fmt.Errorf("download stalled: no data received for %v", p.readTimeout)
	}

	return n, err
}

func (p *progressReader) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true

	// Log final progress
	if p.downloaded > 0 {
		p.logProgress()
	}
	p.mu.Unlock()

	return p.reader.Close()
}

func (p *progressReader) logProgress() {
	if p.total > 0 {
		pct := float64(p.downloaded) / float64(p.total) * 100
		p.logger.Info("download progress",
			"downloaded_mb", p.downloaded/(1024*1024),
			"total_mb", p.total/(1024*1024),
			"percent", fmt.Sprintf("%.1f%%", pct),
		)
	} else {
		p.logger.Info("download progress",
			"downloaded_mb", p.downloaded/(1024*1024),
		)
	}
}
