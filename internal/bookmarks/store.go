package bookmarks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type OAuthStore struct {
	UserID       string    `json:"user_id"`
	RefreshToken string    `json:"refresh_token"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func LoadOAuthStore(path string) (*OAuthStore, error) {
	if path == "" {
		return nil, fmt.Errorf("store path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s OAuthStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode oauth store: %w", err)
	}
	return &s, nil
}

func SaveOAuthStore(path string, store OAuthStore) error {
	if path == "" {
		return fmt.Errorf("store path is empty")
	}
	store.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode oauth store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	// 0600: keep secrets private on disk
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write oauth store: %w", err)
	}
	return nil
}

func DeleteOAuthStore(path string) error {
	if path == "" {
		return fmt.Errorf("store path is empty")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

