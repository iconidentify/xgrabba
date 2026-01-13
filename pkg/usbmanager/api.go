package usbmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// APIHandler handles HTTP requests for USB operations.
type APIHandler struct {
	manager       *Manager
	monitor       *DriveMonitor
	formatManager *FormatManager
	apiKey        string
	logger        *slog.Logger
}

// NewAPIHandler creates a new API handler for USB operations.
func NewAPIHandler(manager *Manager, apiKey string, logger *slog.Logger) *APIHandler {
	if logger == nil {
		logger = slog.Default()
	}

	monitor := NewDriveMonitor(manager, logger)
	formatMgr := NewFormatManager(manager, logger)

	return &APIHandler{
		manager:       manager,
		monitor:       monitor,
		formatManager: formatMgr,
		apiKey:        apiKey,
		logger:        logger,
	}
}

// StartMonitor starts the drive monitor.
func (h *APIHandler) StartMonitor(ctx context.Context) error {
	return h.monitor.Start(ctx)
}

// StopMonitor stops the drive monitor.
func (h *APIHandler) StopMonitor() {
	h.monitor.Stop()
}

// RegisterRoutes registers the USB API routes on a chi router.
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Use(h.authMiddleware)
	r.Get("/drives", h.ListDrives)
	r.Get("/drives/events", h.DriveEvents)
	r.Post("/drives/{device}/mount", h.MountDrive)
	r.Post("/drives/{device}/unmount", h.UnmountDrive)
	r.Post("/drives/{device}/format", h.FormatDrive)
	r.Post("/drives/{device}/format/async", h.FormatDriveAsync)
	r.Post("/drives/{device}/rename", h.RenameDrive)
	r.Get("/format/{operationID}", h.FormatProgress)
	r.Post("/format/{operationID}/cancel", h.CancelFormat)
	r.Get("/health", h.Health)
}

// authMiddleware validates the API key.
func (h *APIHandler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.apiKey != "" {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.URL.Query().Get("api_key")
			}
			if key != h.apiKey {
				h.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing API key")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ListDrives returns a list of connected USB drives.
func (h *APIHandler) ListDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := h.manager.ScanDrives(r.Context())
	if err != nil {
		h.logger.Error("failed to scan drives", "error", err)
		h.writeError(w, http.StatusInternalServerError, ErrCodeNoDrives, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, ListDrivesResponse{Drives: drives})
}

// MountDrive mounts a USB drive.
func (h *APIHandler) MountDrive(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")

	var req MountRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
			return
		}
	}

	mountPoint, err := h.manager.Mount(r.Context(), device, req.MountAs)
	if err != nil {
		h.logger.Error("failed to mount drive", "device", device, "error", err)
		if strings.Contains(err.Error(), "not found") {
			h.writeError(w, http.StatusNotFound, ErrCodeDeviceNotFound, err.Error())
		} else if strings.Contains(err.Error(), "busy") {
			h.writeError(w, http.StatusConflict, ErrCodeDeviceBusy, err.Error())
		} else {
			h.writeError(w, http.StatusInternalServerError, ErrCodeMountFailed, err.Error())
		}
		return
	}

	h.writeJSON(w, http.StatusOK, MountResponse{
		Success:    true,
		MountPoint: mountPoint,
		Message:    "Drive mounted successfully",
	})
}

// UnmountDrive safely unmounts a USB drive.
func (h *APIHandler) UnmountDrive(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")

	err := h.manager.Unmount(r.Context(), device)
	if err != nil {
		h.logger.Error("failed to unmount drive", "device", device, "error", err)
		if strings.Contains(err.Error(), "not found") {
			h.writeError(w, http.StatusNotFound, ErrCodeDeviceNotFound, err.Error())
		} else if strings.Contains(err.Error(), "busy") {
			h.writeError(w, http.StatusConflict, ErrCodeDeviceBusy, err.Error())
		} else {
			h.writeError(w, http.StatusInternalServerError, ErrCodeUnmountFailed, err.Error())
		}
		return
	}

	h.writeJSON(w, http.StatusOK, UnmountResponse{
		Success: true,
		Message: "Drive safely unmounted. You can now remove the USB drive.",
	})
}

// FormatDrive formats a USB drive.
func (h *APIHandler) FormatDrive(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")

	var req FormatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// Validate filesystem
	validFS := map[string]bool{"exfat": true, "ext4": true, "ntfs": true}
	if !validFS[strings.ToLower(req.Filesystem)] {
		h.writeError(w, http.StatusBadRequest, ErrCodeInvalidFilesystem,
			"Supported filesystems: exfat, ext4, ntfs")
		return
	}

	// Validate confirm token
	expectedToken := "erase-all-data-on-" + filepath.Base(device)
	if req.ConfirmToken != expectedToken {
		h.writeError(w, http.StatusBadRequest, ErrCodeInvalidToken,
			"Invalid confirmation token. Expected: "+expectedToken)
		return
	}

	err := h.manager.Format(r.Context(), device, req.Filesystem, req.Label, req.ConfirmToken)
	if err != nil {
		h.logger.Error("failed to format drive", "device", device, "error", err)
		if strings.Contains(err.Error(), "not found") {
			h.writeError(w, http.StatusNotFound, ErrCodeDeviceNotFound, err.Error())
		} else if strings.Contains(err.Error(), "confirmation") {
			h.writeError(w, http.StatusBadRequest, ErrCodeInvalidToken, err.Error())
		} else {
			h.writeError(w, http.StatusInternalServerError, ErrCodeFormatFailed, err.Error())
		}
		return
	}

	h.writeJSON(w, http.StatusOK, FormatResponse{
		Success: true,
		Message: "Drive formatted to " + req.Filesystem + " with label " + req.Label,
	})
}

// RenameDrive changes the filesystem label of a drive.
func (h *APIHandler) RenameDrive(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")

	var req RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	if req.Label == "" {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Label is required")
		return
	}

	// Validate label length (exFAT max 11, NTFS max 32, ext4 max 16)
	if len(req.Label) > 32 {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Label too long (max 32 characters)")
		return
	}

	if err := h.manager.Rename(r.Context(), device, req.Label); err != nil {
		h.logger.Error("failed to rename drive", "device", device, "error", err)
		if strings.Contains(err.Error(), "not found") {
			h.writeError(w, http.StatusNotFound, ErrCodeDeviceNotFound, err.Error())
		} else if strings.Contains(err.Error(), "unsupported") {
			h.writeError(w, http.StatusBadRequest, ErrCodeRenameFailed, err.Error())
		} else {
			h.writeError(w, http.StatusInternalServerError, ErrCodeRenameFailed, err.Error())
		}
		return
	}

	h.writeJSON(w, http.StatusOK, RenameResponse{
		Success: true,
		Label:   req.Label,
		Message: "Drive renamed successfully",
	})
}

// Health returns the health status of the USB manager.
func (h *APIHandler) Health(w http.ResponseWriter, r *http.Request) {
	drives, _ := h.manager.ScanDrives(r.Context())

	h.writeJSON(w, http.StatusOK, HealthResponse{
		Status:         "healthy",
		UdevActive:     h.monitor.IsRunning(),
		DrivesDetected: len(drives),
	})
}

// DriveEvents streams drive events via Server-Sent Events.
func (h *APIHandler) DriveEvents(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Start monitor if not running
	if !h.monitor.IsRunning() {
		if err := h.monitor.Start(r.Context()); err != nil {
			h.logger.Error("failed to start monitor", "error", err)
		}
	}

	// Subscribe to events
	events := h.monitor.Subscribe()
	defer h.monitor.Unsubscribe(events)

	// Send initial drive list
	drives, _ := h.manager.ScanDrives(r.Context())
	initData := map[string]interface{}{
		"type":   "init",
		"drives": drives,
	}
	data, _ := json.Marshal(initData)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Send keepalive every 30 seconds
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// FormatDriveAsync starts an async format operation.
func (h *APIHandler) FormatDriveAsync(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")

	var req FormatAsyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}

	// Validate filesystem
	validFS := map[string]bool{"exfat": true, "ext4": true, "ntfs": true}
	if !validFS[strings.ToLower(req.Filesystem)] {
		h.writeError(w, http.StatusBadRequest, ErrCodeInvalidFilesystem,
			"Supported filesystems: exfat, ext4, ntfs")
		return
	}

	opID, err := h.formatManager.StartFormat(device, req.Filesystem, req.Label, req.ConfirmToken)
	if err != nil {
		h.logger.Error("failed to start async format", "device", device, "error", err)
		h.writeError(w, http.StatusBadRequest, ErrCodeFormatFailed, err.Error())
		return
	}

	h.writeJSON(w, http.StatusAccepted, FormatAsyncResponse{
		OperationID: opID,
		Status:      "started",
		Message:     "Format operation started",
	})
}

// FormatProgress returns the progress of a format operation.
func (h *APIHandler) FormatProgress(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "operationID")

	progress, err := h.formatManager.GetProgress(opID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, progress)
}

// CancelFormat cancels an in-progress format operation.
func (h *APIHandler) CancelFormat(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "operationID")

	if err := h.formatManager.CancelFormat(opID); err != nil {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Format operation cancelled",
	})
}

// writeJSON writes a JSON response.
func (h *APIHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError writes an error response.
func (h *APIHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	h.writeJSON(w, status, ErrorResponse{
		Error:   code,
		Code:    code,
		Message: message,
	})
}
