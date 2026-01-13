package usbmanager

import "time"

// Drive represents a connected USB drive.
type Drive struct {
	Device     string `json:"device"`      // Device path (e.g., /dev/sdb)
	Partition  string `json:"partition"`   // Partition path (e.g., /dev/sdb1)
	Label      string `json:"label"`       // Filesystem label
	Filesystem string `json:"filesystem"`  // Filesystem type (exfat, ext4, ntfs)
	SizeBytes  int64  `json:"size_bytes"`  // Total size in bytes
	UsedBytes  int64  `json:"used_bytes"`  // Used space in bytes
	FreeBytes  int64  `json:"free_bytes"`  // Free space in bytes
	MountPoint string `json:"mount_point"` // Current mount point (empty if unmounted)
	IsMounted  bool   `json:"is_mounted"`  // Whether drive is currently mounted
	Vendor     string `json:"vendor"`      // Drive vendor (e.g., SanDisk)
	Model      string `json:"model"`       // Drive model (e.g., Ultra USB 3.0)
	Serial     string `json:"serial"`      // Serial number
}

// DriveEvent represents a USB drive connect/disconnect event.
type DriveEvent struct {
	Type      EventType `json:"type"`
	Device    string    `json:"device"`
	Timestamp time.Time `json:"timestamp"`
}

// EventType represents the type of drive event.
type EventType string

const (
	EventAdded   EventType = "added"
	EventRemoved EventType = "removed"
)

// MountRequest contains parameters for mounting a drive.
type MountRequest struct {
	MountAs string `json:"mount_as,omitempty"` // Optional custom mount name
}

// MountResponse contains the result of a mount operation.
type MountResponse struct {
	Success    bool   `json:"success"`
	MountPoint string `json:"mount_point,omitempty"`
	Message    string `json:"message"`
}

// UnmountResponse contains the result of an unmount operation.
type UnmountResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// FormatRequest contains parameters for formatting a drive.
type FormatRequest struct {
	Filesystem   string `json:"filesystem"`    // Target filesystem (exfat, ext4, ntfs)
	Label        string `json:"label"`         // Filesystem label
	ConfirmToken string `json:"confirm_token"` // Safety token: "erase-all-data-on-{device}"
}

// FormatResponse contains the result of a format operation.
type FormatResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ListDrivesResponse contains the list of detected USB drives.
type ListDrivesResponse struct {
	Drives []Drive `json:"drives"`
}

// HealthResponse contains the health status of the USB manager.
type HealthResponse struct {
	Status         string `json:"status"`
	UdevActive     bool   `json:"udev_active"`
	DrivesDetected int    `json:"drives_detected"`
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes for USB operations.
const (
	ErrCodeNoDrives          = "NO_DRIVES"
	ErrCodeDeviceNotFound    = "DEVICE_NOT_FOUND"
	ErrCodeDeviceBusy        = "DEVICE_BUSY"
	ErrCodeMountFailed       = "MOUNT_FAILED"
	ErrCodeUnmountFailed     = "UNMOUNT_FAILED"
	ErrCodeFormatFailed      = "FORMAT_FAILED"
	ErrCodeInvalidToken      = "INVALID_TOKEN"
	ErrCodeInvalidFilesystem = "INVALID_FILESYSTEM"
	ErrCodeInsufficientSpace = "INSUFFICIENT_SPACE"
	ErrCodePermissionDenied  = "PERMISSION_DENIED"
)
