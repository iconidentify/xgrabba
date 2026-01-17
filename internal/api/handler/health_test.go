package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iconidentify/xgrabba/internal/repository"
)

func TestHealthHandler_Live(t *testing.T) {
	handler := NewHealthHandler(newMockJobRepository())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.Live(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}

	if resp.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
}

func TestHealthHandler_Ready_Success(t *testing.T) {
	repo := newMockJobRepository()
	repo.stats = &repository.QueueStats{
		Queued:     5,
		Processing: 2,
		Completed:  100,
		Failed:     3,
	}
	handler := NewHealthHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	handler.Ready(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}

	if resp.Queue == nil {
		t.Fatal("queue stats should not be nil")
	}

	if resp.Queue.Queued != 5 {
		t.Errorf("queued = %d, want %d", resp.Queue.Queued, 5)
	}
	if resp.Queue.Processing != 2 {
		t.Errorf("processing = %d, want %d", resp.Queue.Processing, 2)
	}
	if resp.Queue.Completed != 100 {
		t.Errorf("completed = %d, want %d", resp.Queue.Completed, 100)
	}
	if resp.Queue.Failed != 3 {
		t.Errorf("failed = %d, want %d", resp.Queue.Failed, 3)
	}
}

func TestHealthHandler_Ready_Error(t *testing.T) {
	repo := newMockJobRepository()
	repo.statsErr = errors.New("database unavailable")
	handler := NewHealthHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	handler.Ready(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "error" {
		t.Errorf("status = %q, want %q", resp.Status, "error")
	}
}

func TestNewHealthHandler(t *testing.T) {
	repo := newMockJobRepository()
	handler := NewHealthHandler(repo)

	if handler == nil {
		t.Fatal("handler should not be nil")
	}
	if handler.jobRepo == nil {
		t.Error("jobRepo should not be nil")
	}
}

func TestHealthHandler_Stats(t *testing.T) {
	repo := newMockJobRepository()
	handler := NewHealthHandler(repo)

	// Set a known storage path for testing
	t.Setenv("STORAGE_PATH", t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	w := httptest.NewRecorder()

	handler.Stats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	var stats SystemStats
	if err := json.NewDecoder(w.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Basic validation
	if stats.StoragePath == "" {
		t.Error("storage path should not be empty")
	}
}

func TestGetArchiveStats(t *testing.T) {
	// Create a temp directory with some test files
	tmpDir := t.TempDir()

	// Test with empty directory
	videoBytes, videoCount, imageBytes, imageCount, otherBytes, tweetCount := getArchiveStats(tmpDir)

	if videoBytes != 0 {
		t.Errorf("videoBytes = %d, want 0", videoBytes)
	}
	if videoCount != 0 {
		t.Errorf("videoCount = %d, want 0", videoCount)
	}
	if imageBytes != 0 {
		t.Errorf("imageBytes = %d, want 0", imageBytes)
	}
	if imageCount != 0 {
		t.Errorf("imageCount = %d, want 0", imageCount)
	}
	if otherBytes != 0 {
		t.Errorf("otherBytes = %d, want 0", otherBytes)
	}
	if tweetCount != 0 {
		t.Errorf("tweetCount = %d, want 0", tweetCount)
	}
}

func TestGetArchiveStats_NonExistentPath(t *testing.T) {
	// Test with non-existent path - should not panic
	videoBytes, videoCount, imageBytes, imageCount, otherBytes, tweetCount := getArchiveStats("/non/existent/path")

	// Should return zeros without error
	if videoBytes != 0 || videoCount != 0 || imageBytes != 0 || imageCount != 0 || otherBytes != 0 || tweetCount != 0 {
		t.Error("should return all zeros for non-existent path")
	}
}
