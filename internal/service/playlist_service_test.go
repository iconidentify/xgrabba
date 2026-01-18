package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/internal/repository"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func setupPlaylistService(t *testing.T) *PlaylistService {
	t.Helper()
	tmpDir := t.TempDir()
	repo := repository.NewFilesystemPlaylistRepository(tmpDir)
	return NewPlaylistService(repo, nil, testLogger())
}

func TestNewPlaylistService(t *testing.T) {
	svc := setupPlaylistService(t)
	if svc == nil {
		t.Fatal("service should not be nil")
	}
}

func TestPlaylistService_Create(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	playlist, err := svc.Create(ctx, "Test Playlist", "A test description")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if playlist.Name != "Test Playlist" {
		t.Errorf("Name = %q, want %q", playlist.Name, "Test Playlist")
	}
	if playlist.Description != "A test description" {
		t.Errorf("Description = %q, want %q", playlist.Description, "A test description")
	}
	if playlist.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestPlaylistService_Create_TrimsWhitespace(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	playlist, err := svc.Create(ctx, "  Trimmed Name  ", "  Trimmed Description  ")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if playlist.Name != "Trimmed Name" {
		t.Errorf("Name = %q, want %q", playlist.Name, "Trimmed Name")
	}
	if playlist.Description != "Trimmed Description" {
		t.Errorf("Description = %q, want %q", playlist.Description, "Trimmed Description")
	}
}

func TestPlaylistService_Create_EmptyName(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, "", "Description")
	if err != domain.ErrEmptyPlaylistName {
		t.Errorf("expected ErrEmptyPlaylistName, got %v", err)
	}

	// Whitespace-only name should also fail
	_, err = svc.Create(ctx, "   ", "Description")
	if err != domain.ErrEmptyPlaylistName {
		t.Errorf("expected ErrEmptyPlaylistName for whitespace name, got %v", err)
	}
}

func TestPlaylistService_Get(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")

	retrieved, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.Name != "Test" {
		t.Errorf("Name = %q, want %q", retrieved.Name, "Test")
	}
}

func TestPlaylistService_Get_NotFound(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestPlaylistService_List(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	// Empty list
	playlists, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(playlists) != 0 {
		t.Errorf("expected empty list, got %d items", len(playlists))
	}

	// Add some playlists (sleep between creates to avoid ID collision due to second-precision timestamps)
	svc.Create(ctx, "Playlist A", "")
	time.Sleep(1100 * time.Millisecond)
	svc.Create(ctx, "Playlist B", "")

	playlists, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(playlists) != 2 {
		t.Errorf("expected 2 playlists, got %d", len(playlists))
	}
}

func TestPlaylistService_Update(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Original", "Original desc")

	updated, err := svc.Update(ctx, created.ID, "Updated", "New desc")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if updated.Name != "Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated")
	}
	if updated.Description != "New desc" {
		t.Errorf("Description = %q, want %q", updated.Description, "New desc")
	}
}

func TestPlaylistService_Update_EmptyName(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Original", "")

	_, err := svc.Update(ctx, created.ID, "", "New desc")
	if err != domain.ErrEmptyPlaylistName {
		t.Errorf("expected ErrEmptyPlaylistName, got %v", err)
	}
}

func TestPlaylistService_Update_NotFound(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	_, err := svc.Update(ctx, "nonexistent", "Name", "Desc")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestPlaylistService_Delete(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "ToDelete", "")

	err := svc.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Should not be found anymore
	_, err = svc.Get(ctx, created.ID)
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestPlaylistService_Delete_NotFound(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	err := svc.Delete(ctx, "nonexistent")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestPlaylistService_AddItem(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")

	err := svc.AddItem(ctx, created.ID, "tweet-1")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}

	playlist, _ := svc.Get(ctx, created.ID)
	if len(playlist.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(playlist.Items))
	}
	if playlist.Items[0] != "tweet-1" {
		t.Errorf("item = %q, want %q", playlist.Items[0], "tweet-1")
	}
}

func TestPlaylistService_AddItem_Duplicate(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")

	svc.AddItem(ctx, created.ID, "tweet-1")
	err := svc.AddItem(ctx, created.ID, "tweet-1") // Same item again

	// Should not error for duplicate
	if err != nil {
		t.Fatalf("AddItem should not error for duplicate: %v", err)
	}

	playlist, _ := svc.Get(ctx, created.ID)
	if len(playlist.Items) != 1 {
		t.Errorf("should still have 1 item, got %d", len(playlist.Items))
	}
}

func TestPlaylistService_AddItem_NotFound(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	err := svc.AddItem(ctx, "nonexistent", "tweet-1")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestPlaylistService_RemoveItem(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")
	svc.AddItem(ctx, created.ID, "tweet-1")
	svc.AddItem(ctx, created.ID, "tweet-2")

	err := svc.RemoveItem(ctx, created.ID, "tweet-1")
	if err != nil {
		t.Fatalf("RemoveItem failed: %v", err)
	}

	playlist, _ := svc.Get(ctx, created.ID)
	if len(playlist.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(playlist.Items))
	}
	if playlist.Items[0] != "tweet-2" {
		t.Errorf("remaining item = %q, want %q", playlist.Items[0], "tweet-2")
	}
}

func TestPlaylistService_RemoveItem_NotInPlaylist(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")

	err := svc.RemoveItem(ctx, created.ID, "nonexistent-tweet")
	if err != domain.ErrTweetNotInPlaylist {
		t.Errorf("expected ErrTweetNotInPlaylist, got %v", err)
	}
}

func TestPlaylistService_Reorder(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")
	svc.AddItem(ctx, created.ID, "tweet-1")
	svc.AddItem(ctx, created.ID, "tweet-2")
	svc.AddItem(ctx, created.ID, "tweet-3")

	err := svc.Reorder(ctx, created.ID, []string{"tweet-3", "tweet-1", "tweet-2"})
	if err != nil {
		t.Fatalf("Reorder failed: %v", err)
	}

	playlist, _ := svc.Get(ctx, created.ID)
	if playlist.Items[0] != "tweet-3" || playlist.Items[1] != "tweet-1" || playlist.Items[2] != "tweet-2" {
		t.Errorf("order = %v, want [tweet-3, tweet-1, tweet-2]", playlist.Items)
	}
}

func TestPlaylistService_Reorder_WrongLength(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")
	svc.AddItem(ctx, created.ID, "tweet-1")
	svc.AddItem(ctx, created.ID, "tweet-2")

	err := svc.Reorder(ctx, created.ID, []string{"tweet-1"}) // Missing tweet-2
	if err != domain.ErrTweetNotInPlaylist {
		t.Errorf("expected ErrTweetNotInPlaylist, got %v", err)
	}
}

func TestPlaylistService_Reorder_InvalidItem(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, "Test", "")
	svc.AddItem(ctx, created.ID, "tweet-1")
	svc.AddItem(ctx, created.ID, "tweet-2")

	err := svc.Reorder(ctx, created.ID, []string{"tweet-1", "tweet-3"}) // tweet-3 not in playlist
	if err != domain.ErrTweetNotInPlaylist {
		t.Errorf("expected ErrTweetNotInPlaylist, got %v", err)
	}
}

func TestPlaylistService_GetAll(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	svc.Create(ctx, "Playlist A", "")
	time.Sleep(1100 * time.Millisecond)
	svc.Create(ctx, "Playlist B", "")

	playlists, err := svc.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(playlists) != 2 {
		t.Errorf("expected 2 playlists, got %d", len(playlists))
	}
}

func TestPlaylistService_AddToMultiple(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	p1, _ := svc.Create(ctx, "Playlist 1", "")
	p2, _ := svc.Create(ctx, "Playlist 2", "")

	err := svc.AddToMultiple(ctx, []domain.PlaylistID{p1.ID, p2.ID}, "tweet-1")
	if err != nil {
		t.Fatalf("AddToMultiple failed: %v", err)
	}

	// Both playlists should have the tweet
	playlist1, _ := svc.Get(ctx, p1.ID)
	playlist2, _ := svc.Get(ctx, p2.ID)

	if len(playlist1.Items) != 1 || playlist1.Items[0] != "tweet-1" {
		t.Errorf("playlist1 should have tweet-1")
	}
	if len(playlist2.Items) != 1 || playlist2.Items[0] != "tweet-1" {
		t.Errorf("playlist2 should have tweet-1")
	}
}

func TestPlaylistService_AddToMultiple_NotFound(t *testing.T) {
	svc := setupPlaylistService(t)
	ctx := context.Background()

	p1, _ := svc.Create(ctx, "Playlist 1", "")

	err := svc.AddToMultiple(ctx, []domain.PlaylistID{p1.ID, "nonexistent"}, "tweet-1")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestGeneratePlaylistID(t *testing.T) {
	id1 := generatePlaylistID()
	if id1 == "" {
		t.Error("ID should not be empty")
	}

	// IDs should be timestamp-based and look like a date
	if len(id1) != 14 {
		t.Errorf("ID length = %d, expected 14 (YYYYMMDDHHMMSS)", len(id1))
	}
}
