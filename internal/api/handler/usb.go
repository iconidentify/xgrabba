package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/iconidentify/xgrabba/pkg/usbclient"
)

// USBHandler handles USB-related HTTP requests.
type USBHandler struct {
	client *usbclient.Client
	logger *slog.Logger
}

// NewUSBHandler creates a new USB handler.
func NewUSBHandler(client *usbclient.Client, logger *slog.Logger) *USBHandler {
	return &USBHandler{
		client: client,
		logger: logger,
	}
}

// USBDrive represents a USB drive for the frontend.
type USBDrive struct {
	Device     string `json:"device"`
	Label      string `json:"label"`
	Filesystem string `json:"filesystem"`
	SizeBytes  int64  `json:"size_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	MountPoint string `json:"mount_point"`
	IsMounted  bool   `json:"is_mounted"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
}

// ListDrivesResponse is the response for listing USB drives.
type ListDrivesResponse struct {
	Available bool       `json:"available"` // Whether USB Manager is available
	Drives    []USBDrive `json:"drives"`
}

// MountRequest is the request for mounting a USB drive.
type USBMountRequest struct {
	MountAs string `json:"mount_as,omitempty"`
}

// MountResponse is the response for mounting a USB drive.
type USBMountResponse struct {
	Success    bool   `json:"success"`
	MountPoint string `json:"mount_point,omitempty"`
	Message    string `json:"message"`
}

// FormatRequest is the request for formatting a USB drive.
type USBFormatRequest struct {
	Filesystem   string `json:"filesystem"`
	Label        string `json:"label"`
	ConfirmToken string `json:"confirm_token"`
}

// FormatResponse is the response for formatting a USB drive.
type USBFormatResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UnmountResponse is the response for unmounting a USB drive.
type USBUnmountResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// HealthResponse is the response for USB health check.
type USBHealthResponse struct {
	Available      bool   `json:"available"`
	Status         string `json:"status"`
	DrivesDetected int    `json:"drives_detected"`
}

// ListDrives returns a list of connected USB drives.
func (h *USBHandler) ListDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := h.client.ListDrives(r.Context())
	if err != nil {
		h.logger.Warn("failed to list USB drives", "error", err)
		// Return empty list with available=false rather than error
		h.writeJSON(w, http.StatusOK, ListDrivesResponse{
			Available: false,
			Drives:    []USBDrive{},
		})
		return
	}

	response := ListDrivesResponse{
		Available: true,
		Drives:    make([]USBDrive, 0, len(drives)),
	}

	for _, d := range drives {
		response.Drives = append(response.Drives, USBDrive{
			Device:     d.Device,
			Label:      d.Label,
			Filesystem: d.Filesystem,
			SizeBytes:  d.SizeBytes,
			FreeBytes:  d.FreeBytes,
			MountPoint: d.MountPoint,
			IsMounted:  d.IsMounted,
			Vendor:     d.Vendor,
			Model:      d.Model,
		})
	}

	h.writeJSON(w, http.StatusOK, response)
}

// MountDrive mounts a USB drive.
func (h *USBHandler) MountDrive(w http.ResponseWriter, r *http.Request) {
	device := chi.URLParam(r, "device")
	if device == "" {
		h.writeError(w, http.StatusBadRequest, "device parameter is required")
		return
	}

	var req USBMountRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	mountPoint, err := h.client.MountDrive(r.Context(), device, req.MountAs)
	if err != nil {
		h.logger.Error("failed to mount USB drive", "device", device, "error", err)
		h.writeJSON(w, http.StatusInternalServerError, USBMountResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, USBMountResponse{
		Success:    true,
		MountPoint: mountPoint,
		Message:    "Drive mounted successfully",
	})
}

// UnmountDrive safely unmounts a USB drive.
func (h *USBHandler) UnmountDrive(w http.ResponseWriter, r *http.Request) {
	device := chi.URLParam(r, "device")
	if device == "" {
		h.writeError(w, http.StatusBadRequest, "device parameter is required")
		return
	}

	err := h.client.UnmountDrive(r.Context(), device)
	if err != nil {
		h.logger.Error("failed to unmount USB drive", "device", device, "error", err)
		h.writeJSON(w, http.StatusInternalServerError, USBUnmountResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, USBUnmountResponse{
		Success: true,
		Message: "Drive safely unmounted. You can now remove the USB drive.",
	})
}

// FormatDrive formats a USB drive.
func (h *USBHandler) FormatDrive(w http.ResponseWriter, r *http.Request) {
	device := chi.URLParam(r, "device")
	if device == "" {
		h.writeError(w, http.StatusBadRequest, "device parameter is required")
		return
	}

	var req USBFormatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate filesystem
	validFS := map[string]bool{"exfat": true, "ext4": true, "ntfs": true}
	if !validFS[strings.ToLower(req.Filesystem)] {
		h.writeError(w, http.StatusBadRequest, "invalid filesystem; supported: exfat, ext4, ntfs")
		return
	}

	// Validate confirm token format
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}
	expectedToken := "erase-all-data-on-" + filepath.Base(deviceName)
	if req.ConfirmToken != expectedToken {
		h.writeError(w, http.StatusBadRequest, "invalid confirmation token")
		return
	}

	err := h.client.FormatDrive(r.Context(), device, req.Filesystem, req.Label, req.ConfirmToken)
	if err != nil {
		h.logger.Error("failed to format USB drive", "device", device, "error", err)
		h.writeJSON(w, http.StatusInternalServerError, USBFormatResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, USBFormatResponse{
		Success: true,
		Message: "Drive formatted to " + req.Filesystem + " with label " + req.Label,
	})
}

// Health returns the health status of the USB Manager.
func (h *USBHandler) Health(w http.ResponseWriter, r *http.Request) {
	health, err := h.client.Health(r.Context())
	if err != nil {
		h.writeJSON(w, http.StatusOK, USBHealthResponse{
			Available: false,
			Status:    "unavailable",
		})
		return
	}

	h.writeJSON(w, http.StatusOK, USBHealthResponse{
		Available:      true,
		Status:         health.Status,
		DrivesDetected: health.DrivesDetected,
	})
}

// writeJSON writes a JSON response.
func (h *USBHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes an error response.
func (h *USBHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}
