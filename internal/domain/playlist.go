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

// Playlist represents a curated collection of tweets for sequential playback.
type Playlist struct {
	ID          PlaylistID `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Items       []string   `json:"items"` // Tweet IDs in playback order
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ItemCount returns the number of items in the playlist.
func (p *Playlist) ItemCount() int {
	return len(p.Items)
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
