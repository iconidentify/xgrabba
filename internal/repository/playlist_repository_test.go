package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

func TestNewFilesystemPlaylistRepository(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)

	if repo == nil {
		t.Fatal("repo should not be nil")
	}
	if repo.basePath != tmpDir {
		t.Errorf("basePath = %q, want %q", repo.basePath, tmpDir)
	}
	if repo.playlists == nil {
		t.Error("playlists map should be initialized")
	}
}

func TestFilesystemPlaylistRepository_Create(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	playlist := &domain.Playlist{
		ID:          "pl-1",
		Name:        "Test Playlist",
		Description: "A test playlist",
		Items:       []string{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err := repo.Create(ctx, playlist)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify file was created
	storePath := filepath.Join(tmpDir, "playlists.json")
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		t.Error("playlists.json should be created")
	}

	// Verify playlist is in memory
	retrieved, err := repo.Get(ctx, "pl-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.Name != "Test Playlist" {
		t.Errorf("Name = %q, want %q", retrieved.Name, "Test Playlist")
	}
}

func TestFilesystemPlaylistRepository_Create_Duplicate(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	playlist1 := &domain.Playlist{
		ID:   "pl-1",
		Name: "Test Playlist",
	}
	playlist2 := &domain.Playlist{
		ID:   "pl-2",
		Name: "Test Playlist", // Same name
	}

	if err := repo.Create(ctx, playlist1); err != nil {
		t.Fatalf("Create playlist1 failed: %v", err)
	}

	err := repo.Create(ctx, playlist2)
	if err != domain.ErrDuplicatePlaylist {
		t.Errorf("expected ErrDuplicatePlaylist, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_Get(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Create a playlist
	playlist := &domain.Playlist{
		ID:    "pl-1",
		Name:  "Test",
		Items: []string{"tweet-1", "tweet-2"},
	}
	_ = repo.Create(ctx, playlist)

	// Get it back
	retrieved, err := repo.Get(ctx, "pl-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(retrieved.Items) != 2 {
		t.Errorf("Items length = %d, want 2", len(retrieved.Items))
	}
}

func TestFilesystemPlaylistRepository_Get_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_, err := repo.Get(ctx, "nonexistent")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_List(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Empty list first
	playlists, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(playlists) != 0 {
		t.Errorf("expected empty list, got %d items", len(playlists))
	}

	// Add some playlists
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "B Playlist"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-2", Name: "A Playlist"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-3", Name: "C Playlist"})

	playlists, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(playlists) != 3 {
		t.Errorf("expected 3 playlists, got %d", len(playlists))
	}

	// Should be sorted by name
	if playlists[0].Name != "A Playlist" {
		t.Errorf("first playlist should be 'A Playlist', got %q", playlists[0].Name)
	}
}

func TestFilesystemPlaylistRepository_Update(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Create playlist
	playlist := &domain.Playlist{
		ID:   "pl-1",
		Name: "Original Name",
	}
	_ = repo.Create(ctx, playlist)

	// Update it
	playlist.Name = "Updated Name"
	playlist.Description = "Added description"
	err := repo.Update(ctx, playlist)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	retrieved, _ := repo.Get(ctx, "pl-1")
	if retrieved.Name != "Updated Name" {
		t.Errorf("Name = %q, want %q", retrieved.Name, "Updated Name")
	}
	if retrieved.Description != "Added description" {
		t.Errorf("Description = %q, want %q", retrieved.Description, "Added description")
	}
}

func TestFilesystemPlaylistRepository_Update_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	playlist := &domain.Playlist{
		ID:   "nonexistent",
		Name: "Test",
	}

	err := repo.Update(ctx, playlist)
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_Update_DuplicateName(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "Playlist A"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-2", Name: "Playlist B"})

	// Try to rename pl-2 to pl-1's name
	playlist := &domain.Playlist{ID: "pl-2", Name: "Playlist A"}
	err := repo.Update(ctx, playlist)
	if err != domain.ErrDuplicatePlaylist {
		t.Errorf("expected ErrDuplicatePlaylist, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "Test"})

	err := repo.Delete(ctx, "pl-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	_, err = repo.Get(ctx, "pl-1")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_Delete_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	err := repo.Delete(ctx, "nonexistent")
	if err != domain.ErrPlaylistNotFound {
		t.Errorf("expected ErrPlaylistNotFound, got %v", err)
	}
}

func TestFilesystemPlaylistRepository_GetAll(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "Playlist 1"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-2", Name: "Playlist 2"})

	playlists, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	if len(playlists) != 2 {
		t.Errorf("expected 2 playlists, got %d", len(playlists))
	}
}

func TestFilesystemPlaylistRepository_Persistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create repo and add playlist
	repo1 := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	playlist := &domain.Playlist{
		ID:          "pl-1",
		Name:        "Persisted Playlist",
		Description: "Should survive reload",
		Items:       []string{"tweet-1"},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_ = repo1.Create(ctx, playlist)

	// Create new repo instance (simulates restart)
	repo2 := NewFilesystemPlaylistRepository(tmpDir)

	// Should load persisted playlist
	retrieved, err := repo2.Get(ctx, "pl-1")
	if err != nil {
		t.Fatalf("Get failed after reload: %v", err)
	}
	if retrieved.Name != "Persisted Playlist" {
		t.Errorf("Name = %q, want %q", retrieved.Name, "Persisted Playlist")
	}
	if len(retrieved.Items) != 1 {
		t.Errorf("Items length = %d, want 1", len(retrieved.Items))
	}
}

func TestFilesystemPlaylistRepository_Get_ReturnsCopy(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.Playlist{
		ID:    "pl-1",
		Name:  "Test",
		Items: []string{"tweet-1"},
	})

	// Get a copy
	copy1, _ := repo.Get(ctx, "pl-1")
	copy1.Items = append(copy1.Items, "tweet-2")

	// Original should be unchanged
	copy2, _ := repo.Get(ctx, "pl-1")
	if len(copy2.Items) != 1 {
		t.Errorf("modifying copy should not affect stored data, Items = %v", copy2.Items)
	}
}

func TestFilesystemPlaylistRepository_Load_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "playlists.json")

	// Write invalid JSON
	if err := os.WriteFile(storePath, []byte("invalid json{"), 0644); err != nil {
		t.Fatalf("failed to write invalid JSON: %v", err)
	}

	// Should handle invalid JSON gracefully (load should not crash)
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Should be able to create new playlists even if load failed
	playlist := &domain.Playlist{ID: "pl-1", Name: "New Playlist"}
	if err := repo.Create(ctx, playlist); err != nil {
		t.Errorf("should be able to create playlist after invalid JSON load: %v", err)
	}
}

func TestFilesystemPlaylistRepository_Load_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "playlists.json")

	// Write empty file
	if err := os.WriteFile(storePath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Should handle empty file gracefully
	playlists, err := repo.List(ctx)
	if err != nil {
		t.Errorf("List should work with empty file: %v", err)
	}
	if len(playlists) != 0 {
		t.Errorf("expected empty list, got %d", len(playlists))
	}
}

func TestFilesystemPlaylistRepository_Load_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	// Don't create file - should handle missing file gracefully

	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Should work fine with no file
	playlists, err := repo.List(ctx)
	if err != nil {
		t.Errorf("List should work with no file: %v", err)
	}
	if len(playlists) != 0 {
		t.Errorf("expected empty list, got %d", len(playlists))
	}
}

func TestFilesystemPlaylistRepository_Update_SameNameOK(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	playlist := &domain.Playlist{ID: "pl-1", Name: "Test Playlist"}
	_ = repo.Create(ctx, playlist)

	// Updating with same name should be OK
	playlist.Description = "Updated"
	err := repo.Update(ctx, playlist)
	if err != nil {
		t.Errorf("Update with same name should succeed: %v", err)
	}
}

func TestFilesystemPlaylistRepository_List_Sorting(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Add playlists in non-alphabetical order
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-3", Name: "Zebra"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "Apple"})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-2", Name: "Banana"})

	playlists, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(playlists) != 3 {
		t.Fatalf("expected 3 playlists, got %d", len(playlists))
	}

	// Should be sorted alphabetically
	if playlists[0].Name != "Apple" {
		t.Errorf("first = %q, want %q", playlists[0].Name, "Apple")
	}
	if playlists[1].Name != "Banana" {
		t.Errorf("second = %q, want %q", playlists[1].Name, "Banana")
	}
	if playlists[2].Name != "Zebra" {
		t.Errorf("third = %q, want %q", playlists[2].Name, "Zebra")
	}
}

func TestFilesystemPlaylistRepository_GetAll_EmptyItems(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-1", Name: "Empty", Items: []string{}})
	_ = repo.Create(ctx, &domain.Playlist{ID: "pl-2", Name: "With Items", Items: []string{"tweet-1"}})

	playlists, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}

	if len(playlists) != 2 {
		t.Errorf("expected 2 playlists, got %d", len(playlists))
	}

	// Check empty items are preserved
	found := false
	for _, p := range playlists {
		if p.ID == "pl-1" && len(p.Items) != 0 {
			t.Errorf("empty items should be preserved, got %d", len(p.Items))
		}
		if p.ID == "pl-1" {
			found = true
		}
	}
	if !found {
		t.Error("pl-1 should be in GetAll result")
	}
}

func TestFilesystemPlaylistRepository_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	repo := NewFilesystemPlaylistRepository(tmpDir)
	ctx := context.Background()

	// Concurrent creates
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			playlist := &domain.Playlist{
				ID:   domain.PlaylistID(string(rune('a' + id))),
				Name: "Playlist " + string(rune('a'+id)),
			}
			_ = repo.Create(ctx, playlist)
			_, _ = repo.Get(ctx, playlist.ID)
			_, _ = repo.List(ctx)
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all were created
	playlists, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(playlists) < 10 {
		t.Errorf("expected at least 10 playlists, got %d", len(playlists))
	}
}
