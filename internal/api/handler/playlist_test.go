package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/repository"
	"github.com/iconidentify/xgrabba/internal/service"
)

func TestNewPlaylistHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	if handler == nil {
		t.Fatal("handler should not be nil")
	}
	if handler.svc == nil {
		t.Error("svc should not be nil")
	}
}

func TestPlaylistHandler_List(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists", nil)
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var playlists []PlaylistResponse
	if err := json.NewDecoder(w.Body).Decode(&playlists); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if playlists == nil {
		t.Error("playlists should not be nil")
	}
}

func TestPlaylistHandler_Create_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	reqBody := CreatePlaylistRequest{
		Name:        "Test Playlist",
		Description: "Test description",
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp PlaylistResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Name != "Test Playlist" {
		t.Errorf("Name = %q, want Test Playlist", resp.Name)
	}
}

func TestPlaylistHandler_Create_InvalidJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPlaylistHandler_Create_EmptyName(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	reqBody := CreatePlaylistRequest{
		Name: "", // Empty
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPlaylistHandler_Get_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	// Create a playlist first
	playlist, _ := svc.Create(nil, "Test", "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/"+string(playlist.ID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(playlist.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.Get(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp PlaylistResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Name != "Test" {
		t.Errorf("Name = %q, want Test", resp.Name)
	}
}

func TestPlaylistHandler_Get_NotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/nonexistent", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestPlaylistHandler_Delete_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	// Create a playlist first
	playlist, _ := svc.Create(nil, "ToDelete", "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/"+string(playlist.ID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(playlist.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestPlaylistHandler_AddItem_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	// Create a playlist first
	playlist, _ := svc.Create(nil, "Test", "")

	reqBody := map[string]string{"tweet_id": "tweet-123"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/"+string(playlist.ID)+"/items", bytes.NewBuffer(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(playlist.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.AddItem(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestPlaylistHandler_RemoveItem_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := repository.NewFilesystemPlaylistRepository(t.TempDir())
	svc := service.NewPlaylistService(repo, logger)
	handler := NewPlaylistHandler(svc, logger)

	// Create a playlist and add item
	playlist, _ := svc.Create(nil, "Test", "")
	svc.AddItem(nil, playlist.ID, "tweet-123")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/"+string(playlist.ID)+"/items/tweet-123", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", string(playlist.ID))
	rctx.URLParams.Add("tweetId", "tweet-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.RemoveItem(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestToPlaylistResponse(t *testing.T) {
	playlist := &domain.Playlist{
		ID:          domain.PlaylistID("pl-123"),
		Name:        "Test",
		Description: "Desc",
		Items:       []string{"tweet-1", "tweet-2"},
	}

	resp := toPlaylistResponse(playlist)

	if resp.ID != "pl-123" {
		t.Errorf("ID = %q, want pl-123", resp.ID)
	}
	if resp.Name != "Test" {
		t.Errorf("Name = %q, want Test", resp.Name)
	}
	if resp.ItemCount != 2 {
		t.Errorf("ItemCount = %d, want 2", resp.ItemCount)
	}
}
