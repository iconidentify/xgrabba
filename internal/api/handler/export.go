package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/iconidentify/xgrabba/internal/service"
)

// ExportHandler handles export-related HTTP requests.
type ExportHandler struct {
	exportSvc *service.ExportService
	logger    *slog.Logger
}

// NewExportHandler creates a new export handler.
func NewExportHandler(exportSvc *service.ExportService, logger *slog.Logger) *ExportHandler {
	return &ExportHandler{
		exportSvc: exportSvc,
		logger:    logger,
	}
}

// ExportStartRequest is the request body for starting an export.
type ExportStartRequest struct {
	DestPath       string `json:"dest_path"`
	IncludeViewers bool   `json:"include_viewers"`
}

// ExportStartResponse is the response for starting an export.
type ExportStartResponse struct {
	ExportID string `json:"export_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

// ExportStatusResponse is the response for export status.
type ExportStatusResponse struct {
	Active         bool   `json:"active"`
	ExportID       string `json:"export_id,omitempty"`
	Phase          string `json:"phase"`
	TotalTweets    int    `json:"total_tweets"`
	ExportedTweets int    `json:"exported_tweets"`
	BytesWritten   int64  `json:"bytes_written"`
	CurrentFile    string `json:"current_file,omitempty"`
	DestPath       string `json:"dest_path,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Estimate returns an estimate of the export size.
func (h *ExportHandler) Estimate(w http.ResponseWriter, r *http.Request) {
	estimate, err := h.exportSvc.EstimateExport(r.Context())
	if err != nil {
		h.logger.Error("failed to estimate export", "error", err)
		http.Error(w, `{"error": "failed to estimate export"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(estimate)
}

// Start begins an export operation.
func (h *ExportHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req ExportStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.DestPath == "" {
		http.Error(w, `{"error": "dest_path is required"}`, http.StatusBadRequest)
		return
	}

	opts := service.ExportOptions{
		DestPath:       req.DestPath,
		IncludeViewers: req.IncludeViewers,
		ViewerBinDir:   "bin", // Default viewer binary location
	}

	exportID, err := h.exportSvc.StartExportAsync(opts)
	if err != nil {
		if errors.Is(err, service.ErrExportInProgress) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(ExportStartResponse{
				Status:  "conflict",
				Message: "An export is already in progress",
			})
			return
		}
		h.logger.Error("failed to start export", "error", err)
		http.Error(w, `{"error": "failed to start export"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(ExportStartResponse{
		ExportID: exportID,
		Status:   "started",
		Message:  "Export started successfully",
	})
}

// Status returns the current export status.
func (h *ExportHandler) Status(w http.ResponseWriter, r *http.Request) {
	status := h.exportSvc.GetExportStatus()

	active := status.Phase == "preparing" || status.Phase == "exporting" || status.Phase == "finalizing"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ExportStatusResponse{
		Active:         active,
		ExportID:       status.ID,
		Phase:          status.Phase,
		TotalTweets:    status.TotalTweets,
		ExportedTweets: status.ExportedTweets,
		BytesWritten:   status.BytesWritten,
		CurrentFile:    status.CurrentFile,
		DestPath:       status.DestPath,
		Error:          status.Error,
	})
}

// Cancel cancels an in-progress export.
func (h *ExportHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if err := h.exportSvc.CancelExport(); err != nil {
		h.logger.Warn("failed to cancel export", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Export cancellation requested",
	})
}
