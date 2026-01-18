package domain

import (
	"time"
)

// PlaylistID is a unique identifier for a playlist.
type PlaylistID string

// String returns the string representation of the PlaylistID.
func (id PlaylistID) String() string {
	return string(id)
}

// PlaylistType distinguishes between manual and smart playlists.
type PlaylistType string

const (
	// PlaylistTypeManual is a playlist where items are manually added/removed.
	PlaylistTypeManual PlaylistType = "manual"
	// PlaylistTypeSmart is a playlist that is dynamically populated by a search query.
	PlaylistTypeSmart PlaylistType = "smart"
)

// SmartPlaylistConfig holds configuration for smart playlists.
type SmartPlaylistConfig struct {
	Query string `json:"query"`         // Search query to populate the playlist
	Limit int    `json:"limit,omitempty"` // Maximum number of items (default 100)
}

// Playlist represents a curated collection of tweets for sequential playback.
type Playlist struct {
	ID          PlaylistID           `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Type        PlaylistType         `json:"type"`
	SmartConfig *SmartPlaylistConfig `json:"smart_config,omitempty"`
	Items       []string             `json:"items"` // Tweet IDs in playback order
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// ItemCount returns the number of items in the playlist.
func (p *Playlist) ItemCount() int {
	return len(p.Items)
}

// IsSmart returns true if this is a smart playlist (dynamically populated by query).
func (p *Playlist) IsSmart() bool {
	return p.Type == PlaylistTypeSmart
}

// HasItem returns true if the playlist contains the given tweet ID.
func (p *Playlist) HasItem(tweetID string) bool {
	for _, id := range p.Items {
		if id == tweetID {
			return true
		}
	}
	return false
}

// AddItem adds a tweet ID to the end of the playlist if not already present.
func (p *Playlist) AddItem(tweetID string) bool {
	if p.HasItem(tweetID) {
		return false
	}
	p.Items = append(p.Items, tweetID)
	p.UpdatedAt = time.Now()
	return true
}

// RemoveItem removes a tweet ID from the playlist.
func (p *Playlist) RemoveItem(tweetID string) bool {
	for i, id := range p.Items {
		if id == tweetID {
			p.Items = append(p.Items[:i], p.Items[i+1:]...)
			p.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// Reorder updates the order of items in the playlist.
func (p *Playlist) Reorder(newOrder []string) {
	p.Items = newOrder
	p.UpdatedAt = time.Now()
}
