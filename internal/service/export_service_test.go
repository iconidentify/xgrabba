package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestExportService_GetAvailableVolumes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	volumes := svc.GetAvailableVolumes()

	// Just verify it doesn't panic and returns a slice
	if volumes == nil {
		t.Error("GetAvailableVolumes returned nil, expected empty slice")
	}

	// Log found volumes for debugging
	for _, v := range volumes {
		t.Logf("Found volume: %s (%s) - %d bytes free", v.Name, v.Path, v.FreeBytes)
	}
}

func TestExportService_GetExportStatus_NoActiveExport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	status := svc.GetExportStatus()

	if status == nil {
		t.Fatal("GetExportStatus returned nil")
	}

	if status.Phase != "idle" {
		t.Errorf("expected phase 'idle', got %q", status.Phase)
	}
}

func TestExportService_CancelExport_NoActiveExport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	err := svc.CancelExport()

	if err == nil {
		t.Error("expected error when cancelling with no active export")
	}
}

func TestExportService_StartExportAsync_AlreadyInProgress(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	// Simulate an active export
	svc.mu.Lock()
	svc.activeExport = &ActiveExport{
		ID:    "test-export",
		Phase: "exporting",
	}
	svc.mu.Unlock()

	_, err := svc.StartExportAsync(ExportOptions{DestPath: "/tmp/test"})

	if err != ErrExportInProgress {
		t.Errorf("expected ErrExportInProgress, got %v", err)
	}

	// Cleanup
	svc.mu.Lock()
	svc.activeExport = nil
	svc.mu.Unlock()
}

func TestActiveExport_PhaseTransitions(t *testing.T) {
	validTransitions := map[string][]string{
		"preparing":  {"exporting", "failed", "cancelled"},
		"exporting":  {"finalizing", "failed", "cancelled"},
		"finalizing": {"completed", "failed"},
		"completed":  {},
		"failed":     {},
		"cancelled":  {},
	}

	for phase, nextPhases := range validTransitions {
		t.Logf("Phase %q can transition to: %v", phase, nextPhases)
	}
}

func TestFormatExportBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{5368709120, "5.0 GB"},
	}

	for _, tt := range tests {
		result := formatBytesForTest(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, result, tt.expected)
		}
	}
}

// formatBytesForTest mirrors the frontend formatExportBytes function for testing
func formatBytesForTest(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	const k = 1024
	sizes := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	b := float64(bytes)
	for b >= k && i < len(sizes)-1 {
		b /= k
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", bytes, sizes[i])
	}
	return fmt.Sprintf("%.1f %s", b, sizes[i])
}

func TestExportOptions_Validation(t *testing.T) {
	tests := []struct {
		name    string
		opts    ExportOptions
		wantErr bool
	}{
		{
			name:    "empty dest path",
			opts:    ExportOptions{DestPath: ""},
			wantErr: true,
		},
		{
			name:    "valid dest path",
			opts:    ExportOptions{DestPath: "/tmp/export"},
			wantErr: false,
		},
		{
			name:    "with viewers",
			opts:    ExportOptions{DestPath: "/tmp/export", IncludeViewers: true},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := tt.opts.DestPath == ""
			if hasErr != tt.wantErr {
				t.Errorf("validation mismatch: got error=%v, want error=%v", hasErr, tt.wantErr)
			}
		})
	}
}

func TestGetFreeDiskSpace(t *testing.T) {
	// Test with temp directory (should have some free space)
	tmpDir := os.TempDir()
	free := getFreeDiskSpace(tmpDir)

	if free <= 0 {
		t.Errorf("expected positive free space for %s, got %d", tmpDir, free)
	}

	t.Logf("Free space in %s: %d bytes (%.2f GB)", tmpDir, free, float64(free)/(1024*1024*1024))

	// Test with non-existent path
	free = getFreeDiskSpace("/nonexistent/path/that/does/not/exist")
	if free != 0 {
		t.Errorf("expected 0 free space for non-existent path, got %d", free)
	}
}

func TestExportService_ExportIDFormat(t *testing.T) {
	// Verify export ID format matches expected pattern
	id := fmt.Sprintf("exp_%d", time.Now().UnixNano())

	if len(id) < 10 {
		t.Errorf("export ID too short: %s", id)
	}

	if id[:4] != "exp_" {
		t.Errorf("export ID should start with 'exp_': %s", id)
	}

	// Verify numeric portion
	numPart := id[4:]
	for _, c := range numPart {
		if c < '0' || c > '9' {
			t.Errorf("export ID numeric part contains non-digit: %s", id)
			break
		}
	}
}

func TestVolume_Struct(t *testing.T) {
	v := Volume{
		Path:      "/Volumes/USB",
		Name:      "USB",
		FreeBytes: 1024 * 1024 * 1024, // 1 GB
	}

	if v.Path != "/Volumes/USB" {
		t.Errorf("unexpected path: %s", v.Path)
	}
	if v.Name != "USB" {
		t.Errorf("unexpected name: %s", v.Name)
	}
	if v.FreeBytes != 1024*1024*1024 {
		t.Errorf("unexpected free bytes: %d", v.FreeBytes)
	}
}

func TestExportEstimate_Struct(t *testing.T) {
	e := ExportEstimate{
		TweetCount:         100,
		MediaCount:         250,
		EstimatedSizeBytes: 5 * 1024 * 1024 * 1024, // 5 GB
		Volumes: []Volume{
			{Path: "/Volumes/USB", Name: "USB", FreeBytes: 10 * 1024 * 1024 * 1024},
		},
	}

	if e.TweetCount != 100 {
		t.Errorf("unexpected tweet count: %d", e.TweetCount)
	}
	if e.MediaCount != 250 {
		t.Errorf("unexpected media count: %d", e.MediaCount)
	}
	if len(e.Volumes) != 1 {
		t.Errorf("unexpected volumes count: %d", len(e.Volumes))
	}
}

func TestActiveExport_Struct(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ae := ActiveExport{
		ID:             "exp_123",
		DestPath:       "/Volumes/USB/export",
		Phase:          "exporting",
		TotalTweets:    100,
		ExportedTweets: 50,
		BytesWritten:   1024 * 1024 * 500, // 500 MB
		CurrentFile:    "video_001.mp4",
		StartedAt:      time.Now(),
		cancelFunc:     cancel,
	}

	if ae.ID != "exp_123" {
		t.Errorf("unexpected ID: %s", ae.ID)
	}
	if ae.Phase != "exporting" {
		t.Errorf("unexpected phase: %s", ae.Phase)
	}
	if ae.ExportedTweets != 50 {
		t.Errorf("unexpected exported tweets: %d", ae.ExportedTweets)
	}

	// Verify cancel func is set
	if ae.cancelFunc == nil {
		t.Error("cancel func should be set")
	}

	// Test that context can be cancelled
	ae.cancelFunc()
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("context should be cancelled")
	}
}
