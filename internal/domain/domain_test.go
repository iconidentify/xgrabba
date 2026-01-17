package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// =============================================================================
// Tweet Tests
// =============================================================================

func TestTweetID_String(t *testing.T) {
	tests := []struct {
		name string
		id   TweetID
		want string
	}{
		{"simple ID", TweetID("123456"), "123456"},
		{"empty ID", TweetID(""), ""},
		{"long ID", TweetID("1234567890123456789"), "1234567890123456789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("TweetID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTweet_HasMedia(t *testing.T) {
	tests := []struct {
		name  string
		media []Media
		want  bool
	}{
		{"nil media", nil, false},
		{"empty slice", []Media{}, false},
		{"single image", []Media{{Type: MediaTypeImage}}, true},
		{"single video", []Media{{Type: MediaTypeVideo}}, true},
		{"multiple media", []Media{{Type: MediaTypeImage}, {Type: MediaTypeVideo}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &Tweet{Media: tt.media}
			if got := tweet.HasMedia(); got != tt.want {
				t.Errorf("Tweet.HasMedia() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTweet_HasVideo(t *testing.T) {
	tests := []struct {
		name  string
		media []Media
		want  bool
	}{
		{"nil media", nil, false},
		{"empty slice", []Media{}, false},
		{"only images", []Media{{Type: MediaTypeImage}}, false},
		{"has video", []Media{{Type: MediaTypeVideo}}, true},
		{"has gif", []Media{{Type: MediaTypeGIF}}, true},
		{"mixed with video", []Media{{Type: MediaTypeImage}, {Type: MediaTypeVideo}}, true},
		{"multiple images", []Media{{Type: MediaTypeImage}, {Type: MediaTypeImage}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &Tweet{Media: tt.media}
			if got := tweet.HasVideo(); got != tt.want {
				t.Errorf("Tweet.HasVideo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTweet_HasImages(t *testing.T) {
	tests := []struct {
		name  string
		media []Media
		want  bool
	}{
		{"nil media", nil, false},
		{"empty slice", []Media{}, false},
		{"only videos", []Media{{Type: MediaTypeVideo}}, false},
		{"has image", []Media{{Type: MediaTypeImage}}, true},
		{"mixed with image", []Media{{Type: MediaTypeVideo}, {Type: MediaTypeImage}}, true},
		{"gif only", []Media{{Type: MediaTypeGIF}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &Tweet{Media: tt.media}
			if got := tweet.HasImages(); got != tt.want {
				t.Errorf("Tweet.HasImages() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTweet_IsArticle(t *testing.T) {
	tests := []struct {
		name        string
		contentType ContentType
		want        bool
	}{
		{"article", ContentTypeArticle, true},
		{"tweet", ContentTypeTweet, false},
		{"empty", ContentType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &Tweet{ContentType: tt.contentType}
			if got := tweet.IsArticle(); got != tt.want {
				t.Errorf("Tweet.IsArticle() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTweet_CalculateReadingTime(t *testing.T) {
	tests := []struct {
		name      string
		wordCount int
		want      int
	}{
		{"zero words", 0, 0},
		{"negative words", -10, 0},
		{"under one minute", 50, 1},
		{"exactly 200 words", 200, 1},
		{"201 words", 201, 1},
		{"400 words", 400, 2},
		{"500 words", 500, 2},
		{"1000 words", 1000, 5},
		{"2000 words", 2000, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tweet := &Tweet{WordCount: tt.wordCount}
			if got := tweet.CalculateReadingTime(); got != tt.want {
				t.Errorf("Tweet.CalculateReadingTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTweet_ToStoredTweet(t *testing.T) {
	now := time.Now()
	archivedAt := now.Add(-time.Hour)
	replyTo := TweetID("999")
	quotedTweet := TweetID("888")

	tests := []struct {
		name  string
		tweet Tweet
		check func(t *testing.T, stored StoredTweet)
	}{
		{
			name: "basic tweet",
			tweet: Tweet{
				ID:          "123",
				URL:         "https://x.com/user/status/123",
				Author:      Author{Username: "testuser", DisplayName: "Test User"},
				Text:        "Hello world",
				Status:      ArchiveStatusCompleted,
				CreatedAt:   now,
				ArchivedAt:  &archivedAt,
			},
			check: func(t *testing.T, stored StoredTweet) {
				if stored.TweetID != "123" {
					t.Errorf("TweetID = %q, want %q", stored.TweetID, "123")
				}
				if stored.Status != "completed" {
					t.Errorf("Status = %q, want %q", stored.Status, "completed")
				}
				if stored.ArchivedAt != archivedAt {
					t.Errorf("ArchivedAt = %v, want %v", stored.ArchivedAt, archivedAt)
				}
			},
		},
		{
			name: "tweet with reply",
			tweet: Tweet{
				ID:      "123",
				ReplyTo: &replyTo,
			},
			check: func(t *testing.T, stored StoredTweet) {
				if stored.ReplyTo != "999" {
					t.Errorf("ReplyTo = %q, want %q", stored.ReplyTo, "999")
				}
			},
		},
		{
			name: "tweet with quote",
			tweet: Tweet{
				ID:          "123",
				QuotedTweet: &quotedTweet,
			},
			check: func(t *testing.T, stored StoredTweet) {
				if stored.QuotedTweet != "888" {
					t.Errorf("QuotedTweet = %q, want %q", stored.QuotedTweet, "888")
				}
			},
		},
		{
			name: "article tweet",
			tweet: Tweet{
				ID:             "123",
				ContentType:    ContentTypeArticle,
				ArticleTitle:   "My Article",
				ArticleBody:    "Article content here",
				WordCount:      500,
				ReadingMinutes: 3,
			},
			check: func(t *testing.T, stored StoredTweet) {
				if stored.ContentType != "article" {
					t.Errorf("ContentType = %q, want %q", stored.ContentType, "article")
				}
				if stored.ArticleTitle != "My Article" {
					t.Errorf("ArticleTitle = %q, want %q", stored.ArticleTitle, "My Article")
				}
				if stored.WordCount != 500 {
					t.Errorf("WordCount = %d, want %d", stored.WordCount, 500)
				}
			},
		},
		{
			name: "tweet without ArchivedAt uses Now",
			tweet: Tweet{
				ID:         "123",
				ArchivedAt: nil,
			},
			check: func(t *testing.T, stored StoredTweet) {
				// ArchivedAt should be close to now (within a second)
				if time.Since(stored.ArchivedAt) > time.Second {
					t.Errorf("ArchivedAt should be close to now, got %v", stored.ArchivedAt)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := tt.tweet.ToStoredTweet()
			tt.check(t, stored)
		})
	}
}

// =============================================================================
// Job Tests
// =============================================================================

func TestJobID_String(t *testing.T) {
	tests := []struct {
		name string
		id   JobID
		want string
	}{
		{"simple ID", JobID("job-123"), "job-123"},
		{"empty ID", JobID(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("JobID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewJob(t *testing.T) {
	job := NewJob("job-1", "video-1", 3)

	if job.ID != "job-1" {
		t.Errorf("ID = %q, want %q", job.ID, "job-1")
	}
	if job.VideoID != "video-1" {
		t.Errorf("VideoID = %q, want %q", job.VideoID, "video-1")
	}
	if job.Status != JobStatusQueued {
		t.Errorf("Status = %q, want %q", job.Status, JobStatusQueued)
	}
	if job.Attempts != 0 {
		t.Errorf("Attempts = %d, want %d", job.Attempts, 0)
	}
	if job.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want %d", job.MaxRetries, 3)
	}
	if job.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestJob_CanRetry(t *testing.T) {
	tests := []struct {
		name       string
		attempts   int
		maxRetries int
		want       bool
	}{
		{"no attempts yet", 0, 3, true},
		{"one attempt", 1, 3, true},
		{"at limit", 3, 3, false},
		{"over limit", 4, 3, false},
		{"no retries allowed", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &Job{Attempts: tt.attempts, MaxRetries: tt.maxRetries}
			if got := job.CanRetry(); got != tt.want {
				t.Errorf("Job.CanRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJob_MarkProcessing(t *testing.T) {
	job := NewJob("job-1", "video-1", 3)
	before := job.UpdatedAt

	time.Sleep(time.Millisecond) // Ensure time difference
	job.MarkProcessing()

	if job.Status != JobStatusProcessing {
		t.Errorf("Status = %q, want %q", job.Status, JobStatusProcessing)
	}
	if !job.UpdatedAt.After(before) {
		t.Error("UpdatedAt should be updated")
	}
}

func TestJob_MarkCompleted(t *testing.T) {
	job := NewJob("job-1", "video-1", 3)
	job.MarkCompleted()

	if job.Status != JobStatusCompleted {
		t.Errorf("Status = %q, want %q", job.Status, JobStatusCompleted)
	}
}

func TestJob_MarkFailed(t *testing.T) {
	tests := []struct {
		name           string
		attempts       int
		maxRetries     int
		expectedStatus JobStatus
	}{
		{"can retry", 0, 3, JobStatusRetrying},
		{"cannot retry", 3, 3, JobStatusFailed},
		{"exactly at limit after increment", 2, 3, JobStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &Job{
				Attempts:   tt.attempts,
				MaxRetries: tt.maxRetries,
			}
			job.MarkFailed("test error")

			if job.Status != tt.expectedStatus {
				t.Errorf("Status = %q, want %q", job.Status, tt.expectedStatus)
			}
			if job.LastError != "test error" {
				t.Errorf("LastError = %q, want %q", job.LastError, "test error")
			}
			if job.Attempts != tt.attempts+1 {
				t.Errorf("Attempts = %d, want %d", job.Attempts, tt.attempts+1)
			}
		})
	}
}

func TestJob_MarkRetrying(t *testing.T) {
	job := NewJob("job-1", "video-1", 3)
	job.MarkRetrying()

	if job.Status != JobStatusRetrying {
		t.Errorf("Status = %q, want %q", job.Status, JobStatusRetrying)
	}
}

// =============================================================================
// Playlist Tests
// =============================================================================

func TestPlaylistID_String(t *testing.T) {
	tests := []struct {
		name string
		id   PlaylistID
		want string
	}{
		{"simple ID", PlaylistID("playlist-123"), "playlist-123"},
		{"empty ID", PlaylistID(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("PlaylistID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlaylist_ItemCount(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  int
	}{
		{"nil items", nil, 0},
		{"empty items", []string{}, 0},
		{"one item", []string{"a"}, 1},
		{"multiple items", []string{"a", "b", "c"}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Playlist{Items: tt.items}
			if got := p.ItemCount(); got != tt.want {
				t.Errorf("Playlist.ItemCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlaylist_HasItem(t *testing.T) {
	tests := []struct {
		name    string
		items   []string
		tweetID string
		want    bool
	}{
		{"found in list", []string{"a", "b", "c"}, "b", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty list", []string{}, "a", false},
		{"nil list", nil, "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Playlist{Items: tt.items}
			if got := p.HasItem(tt.tweetID); got != tt.want {
				t.Errorf("Playlist.HasItem(%q) = %v, want %v", tt.tweetID, got, tt.want)
			}
		})
	}
}

func TestPlaylist_AddItem(t *testing.T) {
	tests := []struct {
		name      string
		items     []string
		tweetID   string
		wantAdded bool
		wantItems []string
	}{
		{"add to empty", nil, "a", true, []string{"a"}},
		{"add new item", []string{"a", "b"}, "c", true, []string{"a", "b", "c"}},
		{"duplicate item", []string{"a", "b"}, "a", false, []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Playlist{Items: tt.items}
			before := p.UpdatedAt

			time.Sleep(time.Millisecond)
			added := p.AddItem(tt.tweetID)

			if added != tt.wantAdded {
				t.Errorf("AddItem() returned %v, want %v", added, tt.wantAdded)
			}
			if len(p.Items) != len(tt.wantItems) {
				t.Errorf("Items length = %d, want %d", len(p.Items), len(tt.wantItems))
			}
			if added && !p.UpdatedAt.After(before) {
				t.Error("UpdatedAt should be updated when item is added")
			}
		})
	}
}

func TestPlaylist_RemoveItem(t *testing.T) {
	tests := []struct {
		name        string
		items       []string
		tweetID     string
		wantRemoved bool
		wantItems   []string
	}{
		{"remove existing", []string{"a", "b", "c"}, "b", true, []string{"a", "c"}},
		{"remove first", []string{"a", "b", "c"}, "a", true, []string{"b", "c"}},
		{"remove last", []string{"a", "b", "c"}, "c", true, []string{"a", "b"}},
		{"remove non-existent", []string{"a", "b"}, "c", false, []string{"a", "b"}},
		{"remove from empty", nil, "a", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Playlist{Items: tt.items}
			removed := p.RemoveItem(tt.tweetID)

			if removed != tt.wantRemoved {
				t.Errorf("RemoveItem() returned %v, want %v", removed, tt.wantRemoved)
			}
			if len(p.Items) != len(tt.wantItems) {
				t.Errorf("Items length = %d, want %d", len(p.Items), len(tt.wantItems))
			}
		})
	}
}

func TestPlaylist_Reorder(t *testing.T) {
	p := &Playlist{Items: []string{"a", "b", "c"}}
	before := p.UpdatedAt

	time.Sleep(time.Millisecond)
	p.Reorder([]string{"c", "a", "b"})

	if p.Items[0] != "c" || p.Items[1] != "a" || p.Items[2] != "b" {
		t.Errorf("Items = %v, want [c, a, b]", p.Items)
	}
	if !p.UpdatedAt.After(before) {
		t.Error("UpdatedAt should be updated")
	}
}

// =============================================================================
// Event Tests
// =============================================================================

func TestEventID_String(t *testing.T) {
	tests := []struct {
		name string
		id   EventID
		want string
	}{
		{"simple ID", EventID("evt-123"), "evt-123"},
		{"empty ID", EventID(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("EventID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventMetadata_ToJSON(t *testing.T) {
	tests := []struct {
		name     string
		metadata EventMetadata
		wantNil  bool
	}{
		{"nil metadata", nil, true},
		{"empty metadata", EventMetadata{}, false},
		{"with data", EventMetadata{"key": "value", "count": 42}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.metadata.ToJSON()
			if tt.wantNil && result != nil {
				t.Errorf("ToJSON() = %v, want nil", result)
			}
			if !tt.wantNil && result == nil {
				t.Error("ToJSON() = nil, want non-nil")
			}
		})
	}
}

func TestEventMetadata_ToJSON_Roundtrip(t *testing.T) {
	original := EventMetadata{
		"string_val": "hello",
		"int_val":    42,
		"bool_val":   true,
	}

	jsonData := original.ToJSON()
	if jsonData == nil {
		t.Fatal("ToJSON() returned nil")
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded["string_val"] != "hello" {
		t.Errorf("string_val = %v, want 'hello'", decoded["string_val"])
	}
}

// =============================================================================
// Video Tests
// =============================================================================

func TestVideoID_String(t *testing.T) {
	tests := []struct {
		name string
		id   VideoID
		want string
	}{
		{"simple ID", VideoID("vid-123"), "vid-123"},
		{"empty ID", VideoID(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("VideoID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVideo_ToStoredMetadata(t *testing.T) {
	now := time.Now()
	processedAt := now.Add(-time.Hour)

	tests := []struct {
		name  string
		video Video
		check func(t *testing.T, meta StoredMetadata)
	}{
		{
			name: "basic video",
			video: Video{
				ID:       "vid-123",
				TweetURL: "https://x.com/user/status/123",
				TweetID:  "123",
				Filename: "test_video.mp4",
				Metadata: VideoMetadata{
					AuthorUsername: "testuser",
					AuthorName:     "Test User",
					TweetText:      "Hello world",
					Duration:       120,
					Resolution:     "1920x1080",
				},
				ProcessedAt: &processedAt,
			},
			check: func(t *testing.T, meta StoredMetadata) {
				if meta.VideoID != "vid-123" {
					t.Errorf("VideoID = %q, want %q", meta.VideoID, "vid-123")
				}
				if meta.AuthorUsername != "testuser" {
					t.Errorf("AuthorUsername = %q, want %q", meta.AuthorUsername, "testuser")
				}
				if meta.DurationSeconds != 120 {
					t.Errorf("DurationSeconds = %d, want %d", meta.DurationSeconds, 120)
				}
				if meta.ArchivedAt != processedAt {
					t.Errorf("ArchivedAt = %v, want %v", meta.ArchivedAt, processedAt)
				}
			},
		},
		{
			name: "video without ProcessedAt uses Now",
			video: Video{
				ID:          "vid-123",
				ProcessedAt: nil,
			},
			check: func(t *testing.T, meta StoredMetadata) {
				if time.Since(meta.ArchivedAt) > time.Second {
					t.Errorf("ArchivedAt should be close to now, got %v", meta.ArchivedAt)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := tt.video.ToStoredMetadata()
			tt.check(t, meta)
		})
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestVideoError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *VideoError
		wantMsg string
	}{
		{
			name:    "with video ID",
			err:     NewVideoError("vid-123", "download", errors.New("timeout")),
			wantMsg: "download [vid-123]: timeout",
		},
		{
			name:    "without video ID",
			err:     NewVideoError("", "download", errors.New("timeout")),
			wantMsg: "download: timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("VideoError.Error() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestVideoError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	err := NewVideoError("vid-123", "download", inner)

	if got := err.Unwrap(); got != inner {
		t.Errorf("Unwrap() = %v, want %v", got, inner)
	}

	// Test errors.Is works correctly
	if !errors.Is(err, inner) {
		t.Error("errors.Is should return true for inner error")
	}
}

func TestNewVideoError(t *testing.T) {
	inner := errors.New("test error")
	err := NewVideoError("vid-123", "save", inner)

	if err.VideoID != "vid-123" {
		t.Errorf("VideoID = %q, want %q", err.VideoID, "vid-123")
	}
	if err.Op != "save" {
		t.Errorf("Op = %q, want %q", err.Op, "save")
	}
	if err.Err != inner {
		t.Errorf("Err = %v, want %v", err.Err, inner)
	}
}

// Test that domain errors are properly defined
func TestDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrVideoNotFound", ErrVideoNotFound},
		{"ErrJobNotFound", ErrJobNotFound},
		{"ErrNoJobs", ErrNoJobs},
		{"ErrDuplicateVideo", ErrDuplicateVideo},
		{"ErrInvalidTweetURL", ErrInvalidTweetURL},
		{"ErrNoMediaURLs", ErrNoMediaURLs},
		{"ErrDownloadFailed", ErrDownloadFailed},
		{"ErrDownloadTimeout", ErrDownloadTimeout},
		{"ErrURLExpired", ErrURLExpired},
		{"ErrStorageFull", ErrStorageFull},
		{"ErrGrokAPIFailed", ErrGrokAPIFailed},
		{"ErrInvalidAPIKey", ErrInvalidAPIKey},
		{"ErrRateLimited", ErrRateLimited},
		{"ErrMediaNotFound", ErrMediaNotFound},
		{"ErrPlaylistNotFound", ErrPlaylistNotFound},
		{"ErrDuplicatePlaylist", ErrDuplicatePlaylist},
		{"ErrEmptyPlaylistName", ErrEmptyPlaylistName},
		{"ErrTweetNotInPlaylist", ErrTweetNotInPlaylist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Error("Error should not be nil")
			}
			if tt.err.Error() == "" {
				t.Error("Error message should not be empty")
			}
		})
	}
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestArchiveStatusValues(t *testing.T) {
	statuses := []ArchiveStatus{
		ArchiveStatusPending,
		ArchiveStatusFetching,
		ArchiveStatusFetched,
		ArchiveStatusDownloading,
		ArchiveStatusDownloaded,
		ArchiveStatusProcessing,
		ArchiveStatusAnalyzing,
		ArchiveStatusCompleted,
		ArchiveStatusFailed,
	}

	for _, s := range statuses {
		if string(s) == "" {
			t.Errorf("ArchiveStatus %v should not be empty", s)
		}
	}
}

func TestJobStatusValues(t *testing.T) {
	statuses := []JobStatus{
		JobStatusQueued,
		JobStatusProcessing,
		JobStatusCompleted,
		JobStatusFailed,
		JobStatusRetrying,
	}

	for _, s := range statuses {
		if string(s) == "" {
			t.Errorf("JobStatus %v should not be empty", s)
		}
	}
}

func TestMediaTypeValues(t *testing.T) {
	types := []MediaType{
		MediaTypeImage,
		MediaTypeVideo,
		MediaTypeGIF,
	}

	for _, mt := range types {
		if string(mt) == "" {
			t.Errorf("MediaType %v should not be empty", mt)
		}
	}
}

func TestContentTypeValues(t *testing.T) {
	types := []ContentType{
		ContentTypeTweet,
		ContentTypeArticle,
	}

	for _, ct := range types {
		if string(ct) == "" {
			t.Errorf("ContentType %v should not be empty", ct)
		}
	}
}

func TestVideoStatusValues(t *testing.T) {
	statuses := []VideoStatus{
		StatusPending,
		StatusDownloading,
		StatusNaming,
		StatusSaving,
		StatusCompleted,
		StatusFailed,
	}

	for _, s := range statuses {
		if string(s) == "" {
			t.Errorf("VideoStatus %v should not be empty", s)
		}
	}
}

func TestEventSeverityValues(t *testing.T) {
	severities := []EventSeverity{
		EventSeverityInfo,
		EventSeverityWarning,
		EventSeverityError,
		EventSeveritySuccess,
	}

	for _, s := range severities {
		if string(s) == "" {
			t.Errorf("EventSeverity %v should not be empty", s)
		}
	}
}

func TestEventCategoryValues(t *testing.T) {
	categories := []EventCategory{
		EventCategoryExport,
		EventCategoryEncryption,
		EventCategoryUSB,
		EventCategoryBookmarks,
		EventCategoryAI,
		EventCategoryDisk,
		EventCategoryTweet,
		EventCategoryNetwork,
		EventCategorySystem,
	}

	for _, c := range categories {
		if string(c) == "" {
			t.Errorf("EventCategory %v should not be empty", c)
		}
	}
}
