package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
	Download       bool   `json:"download"` // If true, creates a downloadable zip instead of writing to dest_path
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
	DownloadReady  bool   `json:"download_ready,omitempty"` // True when zip is ready for download
	StartedAt      int64  `json:"started_at,omitempty"`     // Unix timestamp when export started
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

	opts := service.ExportOptions{
		DestPath:       req.DestPath,
		IncludeViewers: req.IncludeViewers,
		ViewerBinDir:   "bin", // Default viewer binary location
	}

	var exportID string
	var err error

	if req.Download {
		// Download mode: create a zip file for browser download
		exportID, err = h.exportSvc.StartDownloadExportAsync(opts)
	} else {
		// Path mode: write directly to filesystem
		if req.DestPath == "" {
			http.Error(w, `{"error": "dest_path is required when download is false"}`, http.StatusBadRequest)
			return
		}
		exportID, err = h.exportSvc.StartExportAsync(opts)
	}

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
	downloadReady := status.Phase == "completed" && status.ZipPath != ""

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
		DownloadReady:  downloadReady,
		StartedAt:      status.StartedAt.Unix(),
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

// Download streams the completed export zip file to the client.
func (h *ExportHandler) Download(w http.ResponseWriter, r *http.Request) {
	zipPath, err := h.exportSvc.GetDownloadZipPath()
	if err != nil {
		h.logger.Warn("download not available", "error", err)
		http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Open the zip file
	file, err := os.Open(zipPath)
	if err != nil {
		h.logger.Error("failed to open zip file", "path", zipPath, "error", err)
		http.Error(w, `{"error": "failed to read export file"}`, http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Get file info for content length
	stat, err := file.Stat()
	if err != nil {
		h.logger.Error("failed to stat zip file", "path", zipPath, "error", err)
		http.Error(w, `{"error": "failed to read export file"}`, http.StatusInternalServerError)
		return
	}

	// Generate filename with date
	filename := fmt.Sprintf("xgrabba-archive-%s.zip", time.Now().Format("2006-01-02"))

	// Set headers for file download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Cache-Control", "no-cache")

	// Stream the file
	_, err = io.Copy(w, file)
	if err != nil {
		h.logger.Error("failed to stream zip file", "error", err)
		return
	}

	// Clean up the zip file after successful download
	// Do this in a goroutine to not block the response
	go func() {
		// Small delay to ensure the response is fully sent
		time.Sleep(time.Second)
		h.exportSvc.CleanupDownloadExport()
		h.logger.Info("cleaned up export zip file", "path", zipPath)
	}()
}

// DownloadDirect serves the zip file directly without cleanup (for resumable downloads).
func (h *ExportHandler) DownloadDirect(w http.ResponseWriter, r *http.Request) {
	zipPath, err := h.exportSvc.GetDownloadZipPath()
	if err != nil {
		h.logger.Warn("download not available", "error", err)
		http.Error(w, fmt.Sprintf(`{"error": "%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Use http.ServeFile for better handling of range requests
	filename := fmt.Sprintf("xgrabba-archive-%s.zip", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, zipPath)
}

// Cleanup explicitly cleans up the export zip file.
func (h *ExportHandler) Cleanup(w http.ResponseWriter, r *http.Request) {
	zipPath, _ := h.exportSvc.GetDownloadZipPath()
	h.exportSvc.CleanupDownloadExport()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "Export cleaned up",
		"zip_path": filepath.Base(zipPath),
	})
}
