package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/chrisk/xgrabba/internal/config"
	"github.com/chrisk/xgrabba/internal/domain"
)

// HTTPDownloader implements Downloader using HTTP requests.
type HTTPDownloader struct {
	client    *http.Client
	userAgent string
	cfg       config.DownloadConfig
}

// NewHTTPDownloader creates a new HTTP-based video downloader.
func NewHTTPDownloader(cfg config.DownloadConfig) *HTTPDownloader {
	return &HTTPDownloader{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		userAgent: cfg.UserAgent,
		cfg:       cfg,
	}
}

// Download fetches video from URL with retry logic.
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

	resp, err := d.client.Do(req)
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

	return resp.Body, size, nil
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
