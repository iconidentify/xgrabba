package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/chrisk/xgrabba/internal/repository"
)

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
