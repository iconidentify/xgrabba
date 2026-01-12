package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"syscall"
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
	DiskUsedBytes int64  `json:"disk_used_bytes"`
	DiskFreeBytes int64  `json:"disk_free_bytes"`
	DiskTotalBytes int64 `json:"disk_total_bytes"`
	DiskUsedPct   float64 `json:"disk_used_pct"`
	StoragePath   string `json:"storage_path"`
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

	var statfs syscall.Statfs_t
	if err := syscall.Statfs(storagePath, &statfs); err == nil {
		stats.DiskTotalBytes = int64(statfs.Blocks) * int64(statfs.Bsize)
		stats.DiskFreeBytes = int64(statfs.Bavail) * int64(statfs.Bsize)
		stats.DiskUsedBytes = stats.DiskTotalBytes - stats.DiskFreeBytes
		if stats.DiskTotalBytes > 0 {
			stats.DiskUsedPct = float64(stats.DiskUsedBytes) / float64(stats.DiskTotalBytes) * 100
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
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
