package usbmanager

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// APIHandler handles HTTP requests for USB operations.
type APIHandler struct {
	manager *Manager
	apiKey  string
	logger  *slog.Logger
}

// NewAPIHandler creates a new API handler for USB operations.
func NewAPIHandler(manager *Manager, apiKey string, logger *slog.Logger) *APIHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIHandler{
		manager: manager,
		apiKey:  apiKey,
		logger:  logger,
	}
}

// RegisterRoutes registers the USB API routes on a chi router.
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Use(h.authMiddleware)
	r.Get("/drives", h.ListDrives)
	r.Post("/drives/{device}/mount", h.MountDrive)
	r.Post("/drives/{device}/unmount", h.UnmountDrive)
	r.Post("/drives/{device}/format", h.FormatDrive)
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

// Health returns the health status of the USB manager.
func (h *APIHandler) Health(w http.ResponseWriter, r *http.Request) {
	drives, _ := h.manager.ScanDrives(r.Context())

	h.writeJSON(w, http.StatusOK, HealthResponse{
		Status:         "healthy",
		UdevActive:     true, // TODO: Check actual udev status
		DrivesDetected: len(drives),
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
