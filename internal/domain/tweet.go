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
	ArchiveStatusFetched     ArchiveStatus = "fetched"     // Metadata retrieved, ready for download
	ArchiveStatusDownloading ArchiveStatus = "downloading"
	ArchiveStatusDownloaded  ArchiveStatus = "downloaded"  // Media downloaded, ready for analysis
	ArchiveStatusProcessing  ArchiveStatus = "processing"  // Legacy - kept for backward compat
	ArchiveStatusAnalyzing   ArchiveStatus = "analyzing"   // AI analysis in progress
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

	// Phase completion timestamps for incremental processing
	FetchedAt    *time.Time // When metadata was retrieved from Twitter
	DownloadedAt *time.Time // When all media finished downloading
	AnalyzedAt   *time.Time // When AI analysis completed

	// Progress tracking for UI
	MediaDownloaded int // Number of media items downloaded so far
	MediaTotal      int // Total media items to download
	AITitle       string   // AI-generated descriptive title
	AISummary     string   // AI-generated summary
	AITags        []string // AI-generated searchable tags
	AIContentType string   // AI-detected content type (documentary, news, etc.)
	AITopics      []string // AI-detected main topics
	CreatedAt     time.Time
	ArchivedAt    *time.Time

	// Article-specific fields (when ContentType == "article")
	ContentType    ContentType    // "tweet" or "article"
	ArticleTitle   string         // Article headline/title (distinct from AI-generated title)
	ArticleHTML    string         // Original HTML content from X
	ArticleBody    string         // Plain text or markdown version of article body
	ArticleImages  []ArticleImage // Inline images within the article body
	WordCount      int            // Word count for articles
	ReadingMinutes int            // Estimated reading time
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
	ID         string    `json:"id"`
	Type       MediaType `json:"type"`
	URL        string    `json:"url"`
	PreviewURL string    `json:"preview_url,omitempty"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	Duration   int       `json:"duration_seconds,omitempty"` // For videos
	Bitrate    int       `json:"bitrate,omitempty"`          // For videos
	AltText    string    `json:"alt_text,omitempty"`
	LocalPath  string    `json:"local_path,omitempty"` // Path after download
	Downloaded bool      `json:"downloaded"`
	// Per-media AI analysis (vision and transcript when applicable)
	AICaption     string   `json:"ai_caption,omitempty"`      // AI-generated media description
	AITags        []string `json:"ai_tags,omitempty"`         // Searchable tags specific to this media
	AIContentType string   `json:"ai_content_type,omitempty"` // Content type for this media
	AITopics      []string `json:"ai_topics,omitempty"`       // Topics specific to this media

	// Transcript fields for videos
	Transcript         string `json:"transcript,omitempty"`          // Full audio transcript
	TranscriptLanguage string `json:"transcript_language,omitempty"` // Detected language (ISO-639-1)

	// Essay fields - AI-generated essays from transcript
	Essay         string `json:"essay,omitempty"`          // Full markdown essay
	EssayTitle    string `json:"essay_title,omitempty"`    // Essay title
	EssayStatus   string `json:"essay_status,omitempty"`   // pending, generating, completed, failed
	EssayError    string `json:"essay_error,omitempty"`    // Error message if generation failed
	EssayWordCount int   `json:"essay_word_count,omitempty"` // Word count of the essay
}

// ArticleImage represents an inline image within an article body.
type ArticleImage struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	LocalPath  string `json:"local_path,omitempty"`
	Alt        string `json:"alt,omitempty"`
	Caption    string `json:"caption,omitempty"`
	Position   int    `json:"position"`  // Order/position in article
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Downloaded bool   `json:"downloaded"`
}

// MediaType represents the type of media.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
	MediaTypeGIF   MediaType = "gif"
)

// ContentType distinguishes between regular tweets and long-form articles.
type ContentType string

const (
	ContentTypeTweet   ContentType = "tweet"
	ContentTypeArticle ContentType = "article"
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
	CreatedAt     time.Time    `json:"created_at"`               // When archive was first requested
	ArchivedAt    time.Time    `json:"archived_at"`              // When archive processing completed
	Media         []Media      `json:"media"`
	Metrics       TweetMetrics `json:"metrics"`
	ReplyTo       string       `json:"reply_to,omitempty"`
	QuotedTweet   string       `json:"quoted_tweet,omitempty"`

	// Processing status and phase tracking
	Status          string     `json:"status"`
	FetchedAt       *time.Time `json:"fetched_at,omitempty"`
	DownloadedAt    *time.Time `json:"downloaded_at,omitempty"`
	AnalyzedAt      *time.Time `json:"analyzed_at,omitempty"`
	MediaDownloaded int        `json:"media_downloaded,omitempty"`
	MediaTotal      int        `json:"media_total,omitempty"`

	// AI-generated metadata
	AITitle       string   `json:"ai_title"`
	AISummary     string   `json:"ai_summary,omitempty"`
	AITags        []string `json:"ai_tags,omitempty"`
	AIContentType string   `json:"ai_content_type,omitempty"`
	AITopics      []string `json:"ai_topics,omitempty"`

	// Article-specific fields (when content_type == "article")
	ContentType    string         `json:"content_type,omitempty"`
	ArticleTitle   string         `json:"article_title,omitempty"`
	ArticleHTML    string         `json:"article_html,omitempty"`
	ArticleBody    string         `json:"article_body,omitempty"`
	ArticleImages  []ArticleImage `json:"article_images,omitempty"`
	WordCount      int            `json:"word_count,omitempty"`
	ReadingMinutes int            `json:"reading_minutes,omitempty"`
}

// ToStoredTweet converts a Tweet to StoredTweet for JSON serialization.
func (t *Tweet) ToStoredTweet() StoredTweet {
	archivedAt := time.Now()
	if t.ArchivedAt != nil {
		archivedAt = *t.ArchivedAt
	}

	st := StoredTweet{
		TweetID:         t.ID.String(),
		URL:             t.URL,
		Author:          t.Author,
		Text:            t.Text,
		PostedAt:        t.PostedAt,
		CreatedAt:       t.CreatedAt,
		ArchivedAt:      archivedAt,
		Media:           t.Media,
		Metrics:         t.Metrics,
		Status:          string(t.Status),
		FetchedAt:       t.FetchedAt,
		DownloadedAt:    t.DownloadedAt,
		AnalyzedAt:      t.AnalyzedAt,
		MediaDownloaded: t.MediaDownloaded,
		MediaTotal:      t.MediaTotal,
		AITitle:         t.AITitle,
		AISummary:       t.AISummary,
		AITags:          t.AITags,
		AIContentType:   t.AIContentType,
		AITopics:        t.AITopics,
		// Article fields
		ContentType:    string(t.ContentType),
		ArticleTitle:   t.ArticleTitle,
		ArticleHTML:    t.ArticleHTML,
		ArticleBody:    t.ArticleBody,
		ArticleImages:  t.ArticleImages,
		WordCount:      t.WordCount,
		ReadingMinutes: t.ReadingMinutes,
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

// IsArticle returns true if this is a long-form article rather than a regular tweet.
func (t *Tweet) IsArticle() bool {
	return t.ContentType == ContentTypeArticle
}

// CalculateReadingTime estimates reading time based on word count (200 words per minute).
func (t *Tweet) CalculateReadingTime() int {
	if t.WordCount <= 0 {
		return 0
	}
	minutes := t.WordCount / 200
	if minutes < 1 {
		return 1
	}
	return minutes
}
