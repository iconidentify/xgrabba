package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/iconidentify/xgrabba/internal/repository"
)

var startTime = time.Now()

// HealthHandler handles health check endpoints.
type HealthHandler struct {
	jobRepo repository.JobRepository
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(jobRepo repository.JobRepository) *HealthHandler {
	return &HealthHandler{
		jobRepo: jobRepo,
	}
}

// HealthResponse is the JSON response for health checks.
type HealthResponse struct {
	Status    string      `json:"status"`
	Timestamp string      `json:"timestamp"`
	Queue     *QueueStats `json:"queue,omitempty"`
}

// QueueStats contains job queue statistics.
type QueueStats struct {
	Queued     int `json:"queued"`
	Processing int `json:"processing"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
}

// Live handles GET /health - liveness probe.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready handles GET /ready - readiness probe.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Check job repository is accessible
	stats, err := h.jobRepo.Stats(ctx)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "error",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Queue: &QueueStats{
			Queued:     stats.Queued,
			Processing: stats.Processing,
			Completed:  stats.Completed,
			Failed:     stats.Failed,
		},
	})
}

// SystemStats contains system resource statistics.
type SystemStats struct {
	Uptime        int64  `json:"uptime_seconds"`
	UptimeHuman   string `json:"uptime_human"`
	MemAllocMB    int64  `json:"mem_alloc_mb"`
	MemSysMB      int64  `json:"mem_sys_mb"`
	MemHeapMB     int64  `json:"mem_heap_mb"`
	NumGoroutines int    `json:"num_goroutines"`
	NumCPU        int    `json:"num_cpu"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	DiskFreeBytes  int64   `json:"disk_free_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	DiskUsedPct    float64 `json:"disk_used_pct"`
	StoragePath    string  `json:"storage_path"`

	// Archive storage breakdown
	ArchiveTotalBytes int64 `json:"archive_total_bytes"`
	ArchiveTotalMB    int64 `json:"archive_total_mb"`
	VideoBytes        int64 `json:"video_bytes"`
	VideoMB           int64 `json:"video_mb"`
	ImageBytes        int64 `json:"image_bytes"`
	ImageMB           int64 `json:"image_mb"`
	OtherBytes        int64 `json:"other_bytes"`
	OtherMB           int64 `json:"other_mb"`
	TweetCount        int   `json:"tweet_count"`
}

// Stats handles GET /api/v1/stats - system statistics.
func (h *HealthHandler) Stats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := time.Since(startTime)
	uptimeStr := formatUptime(uptime)

	stats := SystemStats{
		Uptime:        int64(uptime.Seconds()),
		UptimeHuman:   uptimeStr,
		MemAllocMB:    int64(m.Alloc / 1024 / 1024),
		MemSysMB:      int64(m.Sys / 1024 / 1024),
		MemHeapMB:     int64(m.HeapAlloc / 1024 / 1024),
		NumGoroutines: runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
	}

	// Get disk stats for storage path
	storagePath := os.Getenv("STORAGE_PATH")
	if storagePath == "" {
		storagePath = "/data/videos"
	}
	stats.StoragePath = storagePath

	stats.DiskTotalBytes, stats.DiskFreeBytes, stats.DiskUsedBytes, stats.DiskUsedPct = getDiskStats(storagePath)

	// Calculate archive storage breakdown
	stats.VideoBytes, stats.ImageBytes, stats.OtherBytes, stats.TweetCount = getArchiveStats(storagePath)
	stats.ArchiveTotalBytes = stats.VideoBytes + stats.ImageBytes + stats.OtherBytes
	stats.ArchiveTotalMB = stats.ArchiveTotalBytes / 1024 / 1024
	stats.VideoMB = stats.VideoBytes / 1024 / 1024
	stats.ImageMB = stats.ImageBytes / 1024 / 1024
	stats.OtherMB = stats.OtherBytes / 1024 / 1024

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// getArchiveStats walks the storage directory and calculates size breakdown.
func getArchiveStats(storagePath string) (videoBytes, imageBytes, otherBytes int64, tweetCount int) {
	// Video extensions
	videoExts := map[string]bool{".mp4": true, ".webm": true, ".mov": true, ".avi": true, ".mkv": true}
	// Image extensions
	imageExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

	// Track unique tweet directories
	tweetDirs := make(map[string]bool)

	filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Track tweet directories (look for tweet.json)
		if info.Name() == "tweet.json" {
			tweetDirs[filepath.Dir(path)] = true
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		size := info.Size()

		if videoExts[ext] {
			videoBytes += size
		} else if imageExts[ext] {
			imageBytes += size
		} else {
			otherBytes += size
		}

		return nil
	})

	tweetCount = len(tweetDirs)
	return
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
