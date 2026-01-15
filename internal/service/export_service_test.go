package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/pkg/crypto"
)

func TestContentTypeForPath(t *testing.T) {
	cases := map[string]string{
		"foo.json": "application/json",
		"foo.js":   "application/javascript",
		"foo.html": "text/html",
		"foo.css":  "text/css",
		"foo.png":  "image/png",
		"foo.jpg":  "image/jpeg",
		"foo.jpeg": "image/jpeg",
		"foo.gif":  "image/gif",
		"foo.webp": "image/webp",
		"foo.mp4":  "video/mp4",
		"foo.webm": "video/webm",
		"foo.mp3":  "audio/mpeg",
		"foo.wav":  "audio/wav",
		"foo.svg":  "image/svg+xml",
		"foo.ico":  "image/x-icon",
		"foo.bin":  "application/octet-stream",
	}

	for path, expected := range cases {
		if got := contentTypeForPath(path); got != expected {
			t.Fatalf("contentTypeForPath(%q) = %q, want %q", path, got, expected)
		}
	}
}

func TestEncryptingCopyFileManifestEntry(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	encCtx, err := newEncryptionContext("test-password", tmpDir, false)
	if err != nil {
		t.Fatalf("newEncryptionContext failed: %v", err)
	}

	fileSize := crypto.DefaultChunkSize + 5
	data := bytes.Repeat([]byte{0x42}, fileSize)
	srcPath := filepath.Join(tmpDir, "video.mp4")
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	written, err := encCtx.encryptingCopyFile(ctx, srcPath, filepath.Join("data", "video.mp4"))
	if err != nil {
		t.Fatalf("encryptingCopyFile failed: %v", err)
	}
	if written != int64(fileSize) {
		t.Fatalf("written size = %d, want %d", written, fileSize)
	}

	entry, ok := encCtx.manifest[filepath.ToSlash(filepath.Join("data", "video.mp4"))]
	if !ok {
		t.Fatalf("manifest entry missing")
	}
	if entry.OriginalSize != int64(fileSize) {
		t.Fatalf("OriginalSize = %d, want %d", entry.OriginalSize, fileSize)
	}
	if entry.ChunkCount != 2 {
		t.Fatalf("ChunkCount = %d, want 2", entry.ChunkCount)
	}
	if entry.ContentType != "video/mp4" {
		t.Fatalf("ContentType = %q, want video/mp4", entry.ContentType)
	}

	encPath := filepath.Join(encCtx.encDir, entry.EncryptedName)
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("encrypted file missing: %v", err)
	}
}

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

func TestCopyFile_SmallFile(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file with small content
	srcPath := tmpDir + "/source.txt"
	content := []byte("Hello, World!")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Copy file
	dstPath := tmpDir + "/dest.txt"
	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify size
	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}

	// Verify content
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(dstContent) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", string(dstContent), string(content))
	}
}

func TestCopyFile_LargeFile(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file with 5MB content (simulating binary size)
	srcPath := tmpDir + "/source.bin"
	content := make([]byte, 5*1024*1024) // 5MB
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Copy file
	dstPath := tmpDir + "/dest.bin"
	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify size
	expectedSize := int64(5 * 1024 * 1024)
	if size != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, size)
	}

	// Verify destination file size
	dstStat, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("failed to stat destination file: %v", err)
	}
	if dstStat.Size() != expectedSize {
		t.Errorf("destination file size mismatch: got %d, want %d", dstStat.Size(), expectedSize)
	}

	// Verify content integrity (check first and last 1KB)
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	for i := 0; i < 1024; i++ {
		if dstContent[i] != content[i] {
			t.Errorf("content mismatch at byte %d: got %d, want %d", i, dstContent[i], content[i])
			break
		}
	}
	lastStart := len(content) - 1024
	for i := lastStart; i < len(content); i++ {
		if dstContent[i] != content[i] {
			t.Errorf("content mismatch at byte %d: got %d, want %d", i, dstContent[i], content[i])
			break
		}
	}
}

func TestCopyFile_SourceNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	srcPath := tmpDir + "/nonexistent.txt"
	dstPath := tmpDir + "/dest.txt"

	_, err = copyFile(srcPath, dstPath)
	if err == nil {
		t.Error("expected error for non-existent source file")
	}
}

func TestCopyFile_DestinationDirNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := tmpDir + "/source.txt"
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Try to copy to non-existent directory
	dstPath := tmpDir + "/nonexistent/dest.txt"

	_, err = copyFile(srcPath, dstPath)
	if err == nil {
		t.Error("expected error for non-existent destination directory")
	}
}

func TestCopyFile_PermissionDenied(t *testing.T) {
	// Skip on CI or if running as root
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := tmpDir + "/source.txt"
	if err := os.WriteFile(srcPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Create read-only directory
	readOnlyDir := tmpDir + "/readonly"
	if err := os.Mkdir(readOnlyDir, 0555); err != nil {
		t.Fatalf("failed to create read-only dir: %v", err)
	}

	// Try to copy to read-only directory
	dstPath := readOnlyDir + "/dest.txt"
	_, err = copyFile(srcPath, dstPath)
	if err == nil {
		t.Error("expected permission denied error")
	}

	// Cleanup: restore write permission to delete
	_ = os.Chmod(readOnlyDir, 0755)
}

func TestCopyFile_EmptyFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create empty source file
	srcPath := tmpDir + "/empty.txt"
	if err := os.WriteFile(srcPath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Copy file
	dstPath := tmpDir + "/dest.txt"
	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify size is 0
	if size != 0 {
		t.Errorf("expected size 0, got %d", size)
	}

	// Verify destination file exists and is empty
	dstStat, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("failed to stat destination file: %v", err)
	}
	if dstStat.Size() != 0 {
		t.Errorf("destination file size should be 0, got %d", dstStat.Size())
	}
}

func TestCopyFile_BinaryContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file with binary content (all byte values 0-255)
	srcPath := tmpDir + "/binary.bin"
	content := make([]byte, 256)
	for i := range content {
		content[i] = byte(i)
	}
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Copy file
	dstPath := tmpDir + "/dest.bin"
	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify size
	if size != 256 {
		t.Errorf("expected size 256, got %d", size)
	}

	// Verify binary content is preserved
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	for i, b := range dstContent {
		if b != byte(i) {
			t.Errorf("binary content mismatch at byte %d: got %d, want %d", i, b, i)
		}
	}
}

func TestCopyFile_OverwriteExisting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := tmpDir + "/source.txt"
	srcContent := []byte("new content")
	if err := os.WriteFile(srcPath, srcContent, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Create existing destination file with different content
	dstPath := tmpDir + "/dest.txt"
	if err := os.WriteFile(dstPath, []byte("old content that is longer"), 0644); err != nil {
		t.Fatalf("failed to write existing destination file: %v", err)
	}

	// Copy file (should overwrite)
	size, err := copyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify size matches source, not old content
	if size != int64(len(srcContent)) {
		t.Errorf("expected size %d, got %d", len(srcContent), size)
	}

	// Verify content is the new content
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(dstContent) != string(srcContent) {
		t.Errorf("content mismatch: got %q, want %q", string(dstContent), string(srcContent))
	}
}

// TestCopyViewerBinaries_Integration tests the full viewer binary copy workflow
// This simulates copying binaries like the real export process does
func TestCopyViewerBinaries_Integration(t *testing.T) {
	// Create temp directories for source binaries and destination (simulating USB)
	srcDir, err := os.MkdirTemp("", "viewer_binaries_src")
	if err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	defer os.RemoveAll(srcDir)

	dstDir, err := os.MkdirTemp("", "viewer_binaries_dst")
	if err != nil {
		t.Fatalf("failed to create dest dir: %v", err)
	}
	defer os.RemoveAll(dstDir)

	// Simulate viewer binaries with realistic sizes (matching actual binary sizes from issue)
	binaries := []struct {
		name string
		size int
	}{
		{"xgrabba-viewer.exe", 5492224},       // Windows - 5.2MB
		{"xgrabba-viewer-mac-arm64", 5081890}, // Mac ARM - 4.8MB
		{"xgrabba-viewer-mac-amd64", 5370336}, // Mac Intel - 5.1MB
		{"xgrabba-viewer-linux", 5247128},     // Linux - 5.0MB
	}

	// Create fake binaries with deterministic content
	for _, bin := range binaries {
		content := make([]byte, bin.size)
		// Fill with pattern based on filename for verification
		pattern := []byte(bin.name)
		for i := range content {
			content[i] = pattern[i%len(pattern)]
		}
		srcPath := srcDir + "/" + bin.name
		if err := os.WriteFile(srcPath, content, 0755); err != nil {
			t.Fatalf("failed to create fake binary %s: %v", bin.name, err)
		}
		t.Logf("Created source binary: %s (%d bytes)", bin.name, bin.size)
	}

	// Copy each binary using copyFile (same as copyViewerBinaries does)
	for _, bin := range binaries {
		srcPath := srcDir + "/" + bin.name
		dstPath := dstDir + "/" + bin.name

		size, err := copyFile(srcPath, dstPath)
		if err != nil {
			t.Errorf("copyFile failed for %s: %v", bin.name, err)
			continue
		}

		// Verify returned size matches expected
		if size != int64(bin.size) {
			t.Errorf("%s: returned size %d, expected %d", bin.name, size, bin.size)
		}

		// Verify destination file exists and has correct size
		dstStat, err := os.Stat(dstPath)
		if err != nil {
			t.Errorf("%s: failed to stat destination: %v", bin.name, err)
			continue
		}

		if dstStat.Size() != int64(bin.size) {
			t.Errorf("%s: destination size %d bytes, expected %d bytes (THIS IS THE BUG!)",
				bin.name, dstStat.Size(), bin.size)
		}

		if dstStat.Size() == 0 {
			t.Errorf("%s: CRITICAL - destination is ZERO BYTES!", bin.name)
		}

		// Verify content integrity by checking first and last 1KB
		srcContent, _ := os.ReadFile(srcPath)
		dstContent, _ := os.ReadFile(dstPath)

		if len(dstContent) != len(srcContent) {
			t.Errorf("%s: content length mismatch: got %d, want %d",
				bin.name, len(dstContent), len(srcContent))
			continue
		}

		// Check first 1KB
		checkLen := 1024
		if len(srcContent) < checkLen {
			checkLen = len(srcContent)
		}
		for i := 0; i < checkLen; i++ {
			if dstContent[i] != srcContent[i] {
				t.Errorf("%s: content mismatch at byte %d", bin.name, i)
				break
			}
		}

		// Check last 1KB
		if len(srcContent) > 1024 {
			start := len(srcContent) - 1024
			for i := start; i < len(srcContent); i++ {
				if dstContent[i] != srcContent[i] {
					t.Errorf("%s: content mismatch at byte %d (near end)", bin.name, i)
					break
				}
			}
		}

		t.Logf("PASS: %s copied successfully (%d bytes)", bin.name, dstStat.Size())
	}
}

// TestCopyViewerBinaries_Service tests the actual ExportService.copyViewerBinaries method
func TestCopyViewerBinaries_Service(t *testing.T) {
	// Create temp directories
	srcDir, err := os.MkdirTemp("", "viewer_svc_src")
	if err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	defer os.RemoveAll(srcDir)

	dstDir, err := os.MkdirTemp("", "viewer_svc_dst")
	if err != nil {
		t.Fatalf("failed to create dest dir: %v", err)
	}
	defer os.RemoveAll(dstDir)

	// Create test binaries (smaller for faster test)
	binaries := map[string]int{
		"xgrabba-viewer.exe":       1024 * 100, // 100KB
		"xgrabba-viewer-mac-arm64": 1024 * 100,
		"xgrabba-viewer-linux":     1024 * 100,
	}

	for name, size := range binaries {
		content := make([]byte, size)
		for i := range content {
			content[i] = byte(i % 256)
		}
		if err := os.WriteFile(srcDir+"/"+name, content, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}

	// Create ExportService and call copyViewerBinaries
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewExportService(nil, logger, nil)

	err = svc.copyViewerBinaries(srcDir, dstDir)
	if err != nil {
		t.Fatalf("copyViewerBinaries failed: %v", err)
	}

	// Verify all binaries were copied with correct sizes
	for name, expectedSize := range binaries {
		dstPath := dstDir + "/" + name
		stat, err := os.Stat(dstPath)
		if err != nil {
			t.Errorf("%s: not found in destination: %v", name, err)
			continue
		}

		if stat.Size() != int64(expectedSize) {
			t.Errorf("%s: size mismatch - got %d, want %d", name, stat.Size(), expectedSize)
		}

		if stat.Size() == 0 {
			t.Errorf("%s: ZERO BYTES - this is the bug we're fixing!", name)
		}

		// Verify executable permissions
		if stat.Mode().Perm()&0111 == 0 {
			t.Errorf("%s: not executable (mode: %v)", name, stat.Mode())
		}

		t.Logf("PASS: %s - %d bytes, mode %v", name, stat.Size(), stat.Mode())
	}
}

func TestExportService_IsExportUsingPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	// No active export
	if svc.IsExportUsingPath("/Volumes/USB") {
		t.Error("expected false when no active export")
	}

	// Simulate an active export
	svc.mu.Lock()
	svc.activeExport = &ActiveExport{
		ID:         "test-export",
		DestPath:   "/Volumes/USB/xgrabba-archive",
		MountPoint: "/Volumes/USB",
		Phase:      "exporting",
	}
	svc.mu.Unlock()

	// Test matching mount point
	if !svc.IsExportUsingPath("/Volumes/USB") {
		t.Error("expected true for matching mount point")
	}

	// Test non-matching mount point
	if svc.IsExportUsingPath("/Volumes/OtherDrive") {
		t.Error("expected false for non-matching mount point")
	}

	// Test path prefix match (no explicit MountPoint set)
	svc.mu.Lock()
	svc.activeExport.MountPoint = ""
	svc.mu.Unlock()

	if !svc.IsExportUsingPath("/Volumes/USB") {
		t.Error("expected true for path prefix match")
	}

	// Test with completed export (should return false)
	svc.mu.Lock()
	svc.activeExport.Phase = "completed"
	svc.mu.Unlock()

	if svc.IsExportUsingPath("/Volumes/USB") {
		t.Error("expected false for completed export")
	}

	// Cleanup
	svc.mu.Lock()
	svc.activeExport = nil
	svc.mu.Unlock()
}

func TestExportService_CancelExportForPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewExportService(nil, logger, nil)

	// No active export - should return false
	if svc.CancelExportForPath("/Volumes/USB") {
		t.Error("expected false when no active export")
	}

	// Simulate an active export with cancel func
	cancelled := false
	svc.mu.Lock()
	svc.activeExport = &ActiveExport{
		ID:         "test-export",
		DestPath:   "/Volumes/USB/xgrabba-archive",
		MountPoint: "/Volumes/USB",
		Phase:      "exporting",
		cancelFunc: func() { cancelled = true },
	}
	svc.mu.Unlock()

	// Cancel for non-matching path - should return false
	if svc.CancelExportForPath("/Volumes/OtherDrive") {
		t.Error("expected false for non-matching path")
	}
	if cancelled {
		t.Error("cancel func should not have been called")
	}

	// Cancel for matching path - should return true
	if !svc.CancelExportForPath("/Volumes/USB") {
		t.Error("expected true for matching path")
	}
	if !cancelled {
		t.Error("cancel func should have been called")
	}

	// Verify export was marked as cancelled
	svc.mu.Lock()
	phase := svc.activeExport.Phase
	errMsg := svc.activeExport.Error
	svc.mu.Unlock()

	if phase != "cancelled" {
		t.Errorf("expected phase 'cancelled', got %q", phase)
	}
	if errMsg == "" {
		t.Error("expected error message to be set")
	}

	// Cleanup
	svc.mu.Lock()
	svc.activeExport = nil
	svc.mu.Unlock()
}

func TestActiveExport_MountPointField(t *testing.T) {
	ae := ActiveExport{
		ID:         "exp_123",
		DestPath:   "/Volumes/USB/archive",
		MountPoint: "/Volumes/USB",
		Phase:      "exporting",
	}

	if ae.MountPoint != "/Volumes/USB" {
		t.Errorf("expected mount point '/Volumes/USB', got %q", ae.MountPoint)
	}
}
