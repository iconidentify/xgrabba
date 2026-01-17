package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/iconidentify/xgrabba/internal/service"
)

func TestExportHandler_Estimate(t *testing.T) {
	// Skip this test as it requires a fully initialized TweetService
	// The Estimate endpoint is integration tested via end-to-end tests
	t.Skip("Requires TweetService - covered by integration tests")
}

func TestExportHandler_Start_InvalidJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exportSvc := service.NewExportService(nil, nil, logger, nil, "")
	handler := NewExportHandler(exportSvc, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/export/start", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.Start(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestExportHandler_Start_EmptyPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exportSvc := service.NewExportService(nil, nil, logger, nil, "")
	handler := NewExportHandler(exportSvc, logger)

	body, _ := json.Marshal(ExportStartRequest{DestPath: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/export/start", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.Start(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty path, got %d", w.Code)
	}
}

func TestExportHandler_Status_NoActiveExport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exportSvc := service.NewExportService(nil, nil, logger, nil, "")
	handler := NewExportHandler(exportSvc, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/status", nil)
	w := httptest.NewRecorder()

	handler.Status(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp ExportStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Active {
		t.Error("expected active=false when no export in progress")
	}

	if resp.Phase != "idle" {
		t.Errorf("expected phase 'idle', got %q", resp.Phase)
	}
}

func TestExportHandler_Cancel_NoActiveExport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	exportSvc := service.NewExportService(nil, nil, logger, nil, "")
	handler := NewExportHandler(exportSvc, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/export/cancel", nil)
	w := httptest.NewRecorder()

	handler.Cancel(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when no export to cancel, got %d", w.Code)
	}
}

func TestExportStartRequest_JSON(t *testing.T) {
	req := ExportStartRequest{
		DestPath:       "/Volumes/USB/export",
		IncludeViewers: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ExportStartRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.DestPath != req.DestPath {
		t.Errorf("dest_path mismatch: %s != %s", decoded.DestPath, req.DestPath)
	}

	if decoded.IncludeViewers != req.IncludeViewers {
		t.Errorf("include_viewers mismatch: %v != %v", decoded.IncludeViewers, req.IncludeViewers)
	}
}

func TestExportStatusResponse_JSON(t *testing.T) {
	resp := ExportStatusResponse{
		Active:         true,
		ExportID:       "exp_123",
		Phase:          "exporting",
		TotalTweets:    100,
		ExportedTweets: 50,
		BytesWritten:   1024 * 1024 * 500,
		CurrentFile:    "video.mp4",
		DestPath:       "/Volumes/USB/export",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ExportStatusResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Active != resp.Active {
		t.Errorf("active mismatch")
	}
	if decoded.Phase != resp.Phase {
		t.Errorf("phase mismatch")
	}
	if decoded.ExportedTweets != resp.ExportedTweets {
		t.Errorf("exported_tweets mismatch")
	}
}
