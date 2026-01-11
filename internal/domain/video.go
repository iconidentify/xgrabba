package domain

import (
	"time"
)

// VideoID is a unique identifier for a video.
type VideoID string

// String returns the string representation of the VideoID.
func (id VideoID) String() string {
	return string(id)
}

// VideoStatus represents the current processing state of a video.
type VideoStatus string

const (
	StatusPending     VideoStatus = "pending"
	StatusDownloading VideoStatus = "downloading"
	StatusNaming      VideoStatus = "naming"
	StatusSaving      VideoStatus = "saving"
	StatusCompleted   VideoStatus = "completed"
	StatusFailed      VideoStatus = "failed"
)

// Video represents an archived video from X.com.
type Video struct {
	ID          VideoID
	TweetURL    string
	TweetID     string
	MediaURLs   []string
	Metadata    VideoMetadata
	Filename    string
	FilePath    string
	Status      VideoStatus
	Error       string
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

// VideoMetadata contains information about the tweet and video.
type VideoMetadata struct {
	AuthorUsername string    `json:"author_username"`
	AuthorName     string    `json:"author_name"`
	TweetText      string    `json:"tweet_text"`
	PostedAt       time.Time `json:"posted_at"`
	Duration       int       `json:"duration_seconds,omitempty"`
	Resolution     string    `json:"resolution,omitempty"`
	Bitrate        int       `json:"bitrate,omitempty"`
	OriginalURLs   []string  `json:"original_urls"`
	GrokAnalysis   string    `json:"grok_analysis,omitempty"`
}

// StoredMetadata is the JSON structure written alongside the video file.
type StoredMetadata struct {
	VideoID           string    `json:"video_id"`
	TweetURL          string    `json:"tweet_url"`
	TweetID           string    `json:"tweet_id"`
	AuthorUsername    string    `json:"author_username"`
	AuthorName        string    `json:"author_name"`
	TweetText         string    `json:"tweet_text"`
	PostedAt          time.Time `json:"posted_at"`
	ArchivedAt        time.Time `json:"archived_at"`
	DurationSeconds   int       `json:"duration_seconds,omitempty"`
	Resolution        string    `json:"resolution,omitempty"`
	OriginalURLs      []string  `json:"original_urls"`
	GeneratedFilename string    `json:"generated_filename"`
	GrokAnalysis      string    `json:"grok_analysis,omitempty"`
}

// ToStoredMetadata converts a Video to StoredMetadata for JSON serialization.
func (v *Video) ToStoredMetadata() StoredMetadata {
	archivedAt := time.Now()
	if v.ProcessedAt != nil {
		archivedAt = *v.ProcessedAt
	}

	return StoredMetadata{
		VideoID:           v.ID.String(),
		TweetURL:          v.TweetURL,
		TweetID:           v.TweetID,
		AuthorUsername:    v.Metadata.AuthorUsername,
		AuthorName:        v.Metadata.AuthorName,
		TweetText:         v.Metadata.TweetText,
		PostedAt:          v.Metadata.PostedAt,
		ArchivedAt:        archivedAt,
		DurationSeconds:   v.Metadata.Duration,
		Resolution:        v.Metadata.Resolution,
		OriginalURLs:      v.Metadata.OriginalURLs,
		GeneratedFilename: v.Filename,
		GrokAnalysis:      v.Metadata.GrokAnalysis,
	}
}
