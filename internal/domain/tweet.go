package domain

import (
	"time"
)

// TweetID is a unique identifier for a tweet.
type TweetID string

// String returns the string representation of the TweetID.
func (id TweetID) String() string {
	return string(id)
}

// ArchiveStatus represents the current processing state of a tweet archive.
type ArchiveStatus string

const (
	ArchiveStatusPending     ArchiveStatus = "pending"
	ArchiveStatusFetching    ArchiveStatus = "fetching"
	ArchiveStatusDownloading ArchiveStatus = "downloading"
	ArchiveStatusProcessing  ArchiveStatus = "processing"
	ArchiveStatusCompleted   ArchiveStatus = "completed"
	ArchiveStatusFailed      ArchiveStatus = "failed"
)

// Tweet represents an archived tweet from X.com.
type Tweet struct {
	ID            TweetID
	URL           string
	Author        Author
	Text          string
	PostedAt      time.Time
	Media         []Media
	Metrics       TweetMetrics
	ReplyTo       *TweetID // If this is a reply
	QuotedTweet   *TweetID // If this quotes another tweet
	Status        ArchiveStatus
	Error         string
	ArchivePath   string // Base path where tweet is stored
	AITitle       string // AI-generated descriptive title
	AISummary     string // AI-generated summary
	AITags        []string // AI-generated searchable tags
	AIContentType string   // AI-detected content type (documentary, news, etc.)
	AITopics      []string // AI-detected main topics
	CreatedAt     time.Time
	ArchivedAt    *time.Time
}

// Author represents the tweet author with metadata captured at archival time.
type Author struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	AvatarURL      string `json:"avatar_url,omitempty"`
	LocalAvatarURL string `json:"local_avatar_url,omitempty"` // Local copy of avatar
	Verified       bool   `json:"verified,omitempty"`
	FollowerCount  int    `json:"follower_count,omitempty"`
	FollowingCount int    `json:"following_count,omitempty"`
	TweetCount     int    `json:"tweet_count,omitempty"`
	Description    string `json:"description,omitempty"`
}

// Media represents an image or video in a tweet.
type Media struct {
	ID          string    `json:"id"`
	Type        MediaType `json:"type"`
	URL         string    `json:"url"`
	PreviewURL  string    `json:"preview_url,omitempty"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	Duration    int       `json:"duration_seconds,omitempty"` // For videos
	Bitrate     int       `json:"bitrate,omitempty"`          // For videos
	AltText     string    `json:"alt_text,omitempty"`
	LocalPath   string    `json:"local_path,omitempty"` // Path after download
	Downloaded  bool      `json:"downloaded"`
	AICaption   string    `json:"ai_caption,omitempty"` // AI-generated description
}

// MediaType represents the type of media.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
	MediaTypeGIF   MediaType = "gif"
)

// TweetMetrics contains engagement metrics.
type TweetMetrics struct {
	Likes    int `json:"likes"`
	Retweets int `json:"retweets"`
	Replies  int `json:"replies"`
	Views    int `json:"views,omitempty"`
	Quotes   int `json:"quotes,omitempty"`
}

// StoredTweet is the JSON structure written to disk.
type StoredTweet struct {
	TweetID       string       `json:"tweet_id"`
	URL           string       `json:"url"`
	Author        Author       `json:"author"`
	Text          string       `json:"text"`
	PostedAt      time.Time    `json:"posted_at"`
	ArchivedAt    time.Time    `json:"archived_at"`
	Media         []Media      `json:"media"`
	Metrics       TweetMetrics `json:"metrics"`
	ReplyTo       string       `json:"reply_to,omitempty"`
	QuotedTweet   string       `json:"quoted_tweet,omitempty"`
	AITitle       string       `json:"ai_title"`
	AISummary     string       `json:"ai_summary,omitempty"`
	AITags        []string     `json:"ai_tags,omitempty"`
	AIContentType string       `json:"ai_content_type,omitempty"`
	AITopics      []string     `json:"ai_topics,omitempty"`
}

// ToStoredTweet converts a Tweet to StoredTweet for JSON serialization.
func (t *Tweet) ToStoredTweet() StoredTweet {
	archivedAt := time.Now()
	if t.ArchivedAt != nil {
		archivedAt = *t.ArchivedAt
	}

	st := StoredTweet{
		TweetID:       t.ID.String(),
		URL:           t.URL,
		Author:        t.Author,
		Text:          t.Text,
		PostedAt:      t.PostedAt,
		ArchivedAt:    archivedAt,
		Media:         t.Media,
		Metrics:       t.Metrics,
		AITitle:       t.AITitle,
		AISummary:     t.AISummary,
		AITags:        t.AITags,
		AIContentType: t.AIContentType,
		AITopics:      t.AITopics,
	}

	if t.ReplyTo != nil {
		st.ReplyTo = t.ReplyTo.String()
	}
	if t.QuotedTweet != nil {
		st.QuotedTweet = t.QuotedTweet.String()
	}

	return st
}

// HasMedia returns true if the tweet contains any media.
func (t *Tweet) HasMedia() bool {
	return len(t.Media) > 0
}

// HasVideo returns true if the tweet contains video.
func (t *Tweet) HasVideo() bool {
	for _, m := range t.Media {
		if m.Type == MediaTypeVideo || m.Type == MediaTypeGIF {
			return true
		}
	}
	return false
}

// HasImages returns true if the tweet contains images.
func (t *Tweet) HasImages() bool {
	for _, m := range t.Media {
		if m.Type == MediaTypeImage {
			return true
		}
	}
	return false
}
