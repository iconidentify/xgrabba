package usbclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the USB Manager service.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Drive represents a USB drive from the USB Manager.
type Drive struct {
	Device     string `json:"device"`
	Partition  string `json:"partition"`
	Label      string `json:"label"`
	Filesystem string `json:"filesystem"`
	SizeBytes  int64  `json:"size_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	MountPoint string `json:"mount_point"`
	IsMounted  bool   `json:"is_mounted"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	Serial     string `json:"serial"`
}

// ListDrivesResponse is the response from the list drives endpoint.
type ListDrivesResponse struct {
	Drives []Drive `json:"drives"`
}

// MountResponse is the response from the mount endpoint.
type MountResponse struct {
	Success    bool   `json:"success"`
	MountPoint string `json:"mount_point,omitempty"`
	Message    string `json:"message"`
}

// UnmountResponse is the response from the unmount endpoint.
type UnmountResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// FormatResponse is the response from the format endpoint.
type FormatResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// RenameResponse is the response from the rename endpoint.
type RenameResponse struct {
	Success bool   `json:"success"`
	Label   string `json:"label"`
	Message string `json:"message"`
}

// FormatAsyncResponse is the response from the async format endpoint.
type FormatAsyncResponse struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

// FormatProgress is the progress of a format operation.
type FormatProgress struct {
	OperationID   string `json:"operation_id"`
	Device        string `json:"device"`
	Phase         string `json:"phase"`
	Progress      int    `json:"progress"`
	BytesWritten  int64  `json:"bytes_written"`
	TotalBytes    int64  `json:"total_bytes"`
	StartedAt     int64  `json:"started_at"`
	EstimatedSecs int    `json:"estimated_seconds"`
	ElapsedSecs   int    `json:"elapsed_seconds"`
	Error         string `json:"error,omitempty"`
}

// HealthResponse is the response from the health endpoint.
type HealthResponse struct {
	Status         string `json:"status"`
	UdevActive     bool   `json:"udev_active"`
	DrivesDetected int    `json:"drives_detected"`
}

// ErrorResponse represents an error from the USB Manager.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewClient creates a new USB Manager client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Long timeout for format operations
		},
	}
}

// ListDrives returns a list of connected USB drives.
func (c *Client) ListDrives(ctx context.Context) ([]Drive, error) {
	var resp ListDrivesResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/v1/usb/drives", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Drives, nil
}

// MountDrive mounts a USB drive.
func (c *Client) MountDrive(ctx context.Context, device string, mountAs string) (string, error) {
	// Extract device name (e.g., "sdb" from "/dev/sdb")
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	var body string
	if mountAs != "" {
		body = fmt.Sprintf(`{"mount_as":"%s"}`, mountAs)
	}

	var resp MountResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/mount", strings.NewReader(body), &resp); err != nil {
		return "", err
	}

	if !resp.Success {
		return "", fmt.Errorf("mount failed: %s", resp.Message)
	}

	return resp.MountPoint, nil
}

// UnmountDrive safely unmounts a USB drive.
func (c *Client) UnmountDrive(ctx context.Context, device string) error {
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	var resp UnmountResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/unmount", nil, &resp); err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("unmount failed: %s", resp.Message)
	}

	return nil
}

// ForceUnmountDrive forcefully unmounts a USB drive, killing processes if necessary.
func (c *Client) ForceUnmountDrive(ctx context.Context, device string) error {
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	var resp UnmountResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/unmount?force=true", nil, &resp); err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("force unmount failed: %s", resp.Message)
	}

	return nil
}

// FormatDrive formats a USB drive.
func (c *Client) FormatDrive(ctx context.Context, device, filesystem, label, confirmToken string) error {
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	body := fmt.Sprintf(`{"filesystem":"%s","label":"%s","confirm_token":"%s"}`, filesystem, label, confirmToken)

	var resp FormatResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/format", strings.NewReader(body), &resp); err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("format failed: %s", resp.Message)
	}

	return nil
}

// RenameDrive changes the filesystem label of a USB drive.
func (c *Client) RenameDrive(ctx context.Context, device, label string) error {
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	body := fmt.Sprintf(`{"label":"%s"}`, label)

	var resp RenameResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/rename", strings.NewReader(body), &resp); err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("rename failed: %s", resp.Message)
	}

	return nil
}

// Health checks the health of the USB Manager.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/v1/usb/health", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FormatDriveAsync starts an async format operation.
func (c *Client) FormatDriveAsync(ctx context.Context, device, filesystem, label, confirmToken string) (string, error) {
	deviceName := device
	if strings.HasPrefix(device, "/dev/") {
		deviceName = strings.TrimPrefix(device, "/dev/")
	}

	body := fmt.Sprintf(`{"filesystem":"%s","label":"%s","confirm_token":"%s"}`, filesystem, label, confirmToken)

	var resp FormatAsyncResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/usb/drives/"+deviceName+"/format/async", strings.NewReader(body), &resp); err != nil {
		return "", err
	}

	if resp.Status == "error" {
		return "", fmt.Errorf("format failed: %s", resp.Message)
	}

	return resp.OperationID, nil
}

// GetFormatProgress returns the progress of a format operation.
func (c *Client) GetFormatProgress(ctx context.Context, operationID string) (*FormatProgress, error) {
	var resp FormatProgress
	if err := c.doRequest(ctx, http.MethodGet, "/api/v1/usb/format/"+operationID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetBaseURL returns the base URL of the USB Manager.
func (c *Client) GetBaseURL() string {
	return c.baseURL
}

// GetAPIKey returns the API key.
func (c *Client) GetAPIKey() string {
	return c.apiKey
}

// IsAvailable checks if the USB Manager service is reachable.
func (c *Client) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	_, err := c.Health(ctx)
	return err == nil
}

// doRequest performs an HTTP request to the USB Manager.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader, result interface{}) error {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Message != "" {
			return fmt.Errorf("%s: %s", errResp.Code, errResp.Message)
		}
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}
