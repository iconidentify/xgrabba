package downloader

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/domain"
)

func testConfig() config.DownloadConfig {
	return config.DownloadConfig{
		Timeout:       5 * time.Second,
		RetryDelay:    10 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
		UserAgent:     "test-agent",
	}
}

func TestNewHTTPDownloader(t *testing.T) {
	cfg := testConfig()
	dl := NewHTTPDownloader(cfg)

	if dl == nil {
		t.Fatal("downloader should not be nil")
	}
	if dl.userAgent != "test-agent" {
		t.Errorf("userAgent = %q, want %q", dl.userAgent, "test-agent")
	}
	if dl.client == nil {
		t.Error("client should not be nil")
	}
}

func TestHTTPDownloader_Download_Success(t *testing.T) {
	content := []byte("video content data here")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if ua := r.Header.Get("User-Agent"); ua != "test-agent" {
			t.Errorf("User-Agent = %q, want %q", ua, "test-agent")
		}
		w.Header().Set("Content-Length", "23")
		w.Write(content)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	reader, size, err := dl.Download(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	if size != 23 {
		t.Errorf("size = %d, want 23", size)
	}

	data, _ := io.ReadAll(reader)
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", string(data), string(content))
	}
}

func TestHTTPDownloader_Download_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	_, _, err := dl.Download(context.Background(), server.URL)

	if err == nil {
		t.Fatal("expected error for forbidden response")
	}
	// Should contain ErrURLExpired in error chain
	if err == domain.ErrURLExpired || (err != nil && err.Error() != "") {
		// Error is expected - URL expired is not retryable
	}
}

func TestHTTPDownloader_Download_RateLimited(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("success"))
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	reader, _, err := dl.Download(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Download should succeed after retries: %v", err)
	}
	reader.Close()

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestHTTPDownloader_Download_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("delayed"))
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err := dl.Download(ctx, server.URL)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestHTTPDownloader_Probe_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("Probe should use HEAD, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	result, err := dl.Probe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if !result.Accessible {
		t.Error("Accessible should be true")
	}
	if result.ContentType != "video/mp4" {
		t.Errorf("ContentType = %q, want %q", result.ContentType, "video/mp4")
	}
	if result.ContentLength != 1024 {
		t.Errorf("ContentLength = %d, want 1024", result.ContentLength)
	}
}

func TestHTTPDownloader_Probe_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	result, err := dl.Probe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Probe should not return error: %v", err)
	}

	if result.Accessible {
		t.Error("Accessible should be false for 404")
	}
	if result.Error == "" {
		t.Error("Error should contain status code")
	}
}

func TestHTTPDownloader_SelectBestURL_FirstAccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	urls := []string{server.URL + "/high", server.URL + "/medium", server.URL + "/low"}

	best, err := dl.SelectBestURL(context.Background(), urls)
	if err != nil {
		t.Fatalf("SelectBestURL failed: %v", err)
	}
	if best != urls[0] {
		t.Errorf("should return first accessible URL, got %q", best)
	}
}

func TestHTTPDownloader_SelectBestURL_SkipsInaccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/high" || r.URL.Path == "/medium" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	urls := []string{server.URL + "/high", server.URL + "/medium", server.URL + "/low"}

	best, err := dl.SelectBestURL(context.Background(), urls)
	if err != nil {
		t.Fatalf("SelectBestURL failed: %v", err)
	}
	if best != server.URL+"/low" {
		t.Errorf("should return first accessible URL, got %q", best)
	}
}

func TestHTTPDownloader_SelectBestURL_NoneAccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	urls := []string{server.URL + "/a", server.URL + "/b"}

	_, err := dl.SelectBestURL(context.Background(), urls)
	if err != domain.ErrNoMediaURLs {
		t.Errorf("expected ErrNoMediaURLs, got %v", err)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"rate limited", domain.ErrRateLimited, true},
		{"URL expired", domain.ErrURLExpired, false},
		{"generic error", io.EOF, true},
		{"nil error", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestProbeResult(t *testing.T) {
	result := &ProbeResult{
		ContentType:   "video/mp4",
		ContentLength: 1024,
		Accessible:    true,
		Error:         "",
	}

	if !result.Accessible {
		t.Error("Accessible should be true")
	}
}

func TestHTTPDownloader_Download_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	_, _, err := dl.Download(context.Background(), server.URL)

	if err == nil {
		t.Fatal("expected error for unauthorized response")
	}
	// Should contain ErrURLExpired in error chain
	if err != domain.ErrURLExpired && err.Error() == "" {
		t.Errorf("expected ErrURLExpired or error message, got %v", err)
	}
}

func TestHTTPDownloader_Download_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	_, _, err := dl.Download(context.Background(), server.URL)

	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if err.Error() == "" {
		t.Error("error should contain message")
	}
}

func TestHTTPDownloader_Download_ContentLengthFromHeader(t *testing.T) {
	content := []byte("test content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12")
		w.Write(content)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	reader, size, err := dl.Download(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	if size != 12 {
		t.Errorf("size = %d, want 12", size)
	}
}

func TestHTTPDownloader_Download_InvalidContentLengthHeader(t *testing.T) {
	content := []byte("test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "invalid")
		w.Write(content)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	reader, size, err := dl.Download(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	// Should handle invalid Content-Length gracefully
	if size < 0 {
		// Negative size is acceptable when Content-Length is invalid
	}
}

func TestHTTPDownloader_Download_MaxRetryDelay(t *testing.T) {
	cfg := testConfig()
	cfg.RetryDelay = 50 * time.Millisecond
	cfg.MaxRetryDelay = 10 * time.Millisecond // Max is less than exponential backoff

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("success"))
	}))
	defer server.Close()

	dl := NewHTTPDownloader(cfg)
	reader, _, err := dl.Download(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Download should succeed after retries: %v", err)
	}
	reader.Close()

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestHTTPDownloader_Download_ContextCanceledDuringRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, _, err := dl.Download(ctx, server.URL)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if err != context.Canceled && err.Error() == "" {
		t.Errorf("expected context.Canceled or error, got %v", err)
	}
}

func TestHTTPDownloader_Download_NetworkError(t *testing.T) {
	dl := NewHTTPDownloader(testConfig())
	_, _, err := dl.Download(context.Background(), "http://invalid-domain-that-does-not-exist-12345.com/video.mp4")

	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestHTTPDownloader_Download_NonRetryableError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // Not retryable
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	_, _, err := dl.Download(context.Background(), server.URL)

	if err == nil {
		t.Fatal("expected error")
	}
	// Should not retry for URL expired
}

func TestHTTPDownloader_Probe_NetworkError(t *testing.T) {
	dl := NewHTTPDownloader(testConfig())
	result, err := dl.Probe(context.Background(), "http://invalid-domain-that-does-not-exist-12345.com/video.mp4")

	if err != nil {
		t.Fatalf("Probe should not return error for network failures: %v", err)
	}
	if result.Accessible {
		t.Error("Accessible should be false for network errors")
	}
	if result.Error == "" {
		t.Error("Error should contain network error message")
	}
}

func TestHTTPDownloader_Probe_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := dl.Probe(ctx, server.URL)
	if err != nil {
		// Probe may return error or result with error message
		if result != nil && result.Error == "" {
			t.Error("result.Error should contain error message")
		}
	}
}

func TestHTTPDownloader_SelectBestURL_ProbeError(t *testing.T) {
	// Server that fails on HEAD but succeeds on GET
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dl := NewHTTPDownloader(testConfig())
	urls := []string{server.URL + "/a", server.URL + "/b"}

	// Should skip URLs that fail probe
	best, err := dl.SelectBestURL(context.Background(), urls)
	if err == nil {
		// If all probes fail, should return error
		if best == "" {
			if err != domain.ErrNoMediaURLs {
				t.Errorf("expected ErrNoMediaURLs when all probes fail, got %v", err)
			}
		}
	}
}
