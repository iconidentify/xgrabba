package service

import (
	"testing"

	"github.com/iconidentify/xgrabba/internal/domain"
)

func TestIsPermanentTweetFailure(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
		want   bool
	}{
		{
			name:   "empty string",
			errMsg: "",
			want:   false,
		},
		{
			name:   "author data unavailable",
			errMsg: "author data unavailable for tweet",
			want:   true,
		},
		{
			name:   "account suspended",
			errMsg: "account suspended by Twitter",
			want:   true,
		},
		{
			name:   "account is suspended",
			errMsg: "account is suspended and cannot be accessed",
			want:   true,
		},
		{
			name:   "tweet not found",
			errMsg: "Tweet not found in response",
			want:   true,
		},
		{
			name:   "tweet deleted",
			errMsg: "Tweet has been deleted by the author",
			want:   true,
		},
		{
			name:   "protected tweets",
			errMsg: "Protected tweets cannot be accessed",
			want:   true,
		},
		{
			name:   "user not found",
			errMsg: "User not found",
			want:   true,
		},
		{
			name:   "account doesn't exist",
			errMsg: "This account doesn't exist",
			want:   true,
		},
		{
			name:   "temporary error",
			errMsg: "network connection timeout",
			want:   false,
		},
		{
			name:   "rate limited",
			errMsg: "rate limit exceeded",
			want:   false,
		},
		{
			name:   "case insensitive",
			errMsg: "ACCOUNT SUSPENDED",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPermanentTweetFailure(tt.errMsg)
			if got != tt.want {
				t.Errorf("isPermanentTweetFailure(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestBuildTweetPrompt(t *testing.T) {
	tests := []struct {
		name  string
		tweet *domain.Tweet
		want  string
	}{
		{
			name: "text only",
			tweet: &domain.Tweet{
				Text:  "Hello world",
				Media: []domain.Media{},
			},
			want: "Hello world",
		},
		{
			name: "with video",
			tweet: &domain.Tweet{
				Text: "Check this video",
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo},
				},
			},
			want: "Check this video\n[Contains video]",
		},
		{
			name: "with images",
			tweet: &domain.Tweet{
				Text: "Some photos",
				Media: []domain.Media{
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeImage},
				},
			},
			want: "Some photos\n[Contains 2 images]",
		},
		{
			name: "with video and images",
			tweet: &domain.Tweet{
				Text: "Mixed media",
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo},
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeImage},
				},
			},
			want: "Mixed media\n[Contains video]\n[Contains 2 images]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTweetPrompt(tt.tweet)
			if got != tt.want {
				t.Errorf("buildTweetPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetTotalVideoDuration(t *testing.T) {
	tests := []struct {
		name  string
		tweet *domain.Tweet
		want  int
	}{
		{
			name: "no media",
			tweet: &domain.Tweet{
				Media: []domain.Media{},
			},
			want: 0,
		},
		{
			name: "single video",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo, Duration: 30},
				},
			},
			want: 30,
		},
		{
			name: "multiple videos",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo, Duration: 30},
					{Type: domain.MediaTypeVideo, Duration: 45},
				},
			},
			want: 75,
		},
		{
			name: "mixed media",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo, Duration: 60},
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeGIF},
				},
			},
			want: 60,
		},
		{
			name: "images only",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeImage},
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getTotalVideoDuration(tt.tweet)
			if got != tt.want {
				t.Errorf("getTotalVideoDuration() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountImages(t *testing.T) {
	tests := []struct {
		name  string
		tweet *domain.Tweet
		want  int
	}{
		{
			name: "no media",
			tweet: &domain.Tweet{
				Media: []domain.Media{},
			},
			want: 0,
		},
		{
			name: "single image",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeImage},
				},
			},
			want: 1,
		},
		{
			name: "multiple images",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeImage},
				},
			},
			want: 3,
		},
		{
			name: "mixed media",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeVideo},
					{Type: domain.MediaTypeImage},
					{Type: domain.MediaTypeGIF},
				},
			},
			want: 2,
		},
		{
			name: "videos only",
			tweet: &domain.Tweet{
				Media: []domain.Media{
					{Type: domain.MediaTypeVideo},
					{Type: domain.MediaTypeVideo},
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countImages(tt.tweet)
			if got != tt.want {
				t.Errorf("countImages() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPermanentFailurePatterns(t *testing.T) {
	// Ensure all patterns are present
	expectedPatterns := []string{
		"author data unavailable",
		"account suspended",
		"account is suspended",
		"tweet not found",
		"tweet has been deleted",
		"protected tweets",
		"User not found",
		"This account doesn't exist",
	}

	if len(permanentFailurePatterns) != len(expectedPatterns) {
		t.Errorf("expected %d patterns, got %d", len(expectedPatterns), len(permanentFailurePatterns))
	}

	for _, expected := range expectedPatterns {
		found := false
		for _, pattern := range permanentFailurePatterns {
			if pattern == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected pattern: %q", expected)
		}
	}
}

func TestPipelineDiagnostics(t *testing.T) {
	diag := PipelineDiagnostics{
		FFmpegAvailable:    true,
		FFmpegVersion:      "5.1.2",
		VideoProcessorInit: true,
		WhisperEnabled:     false,
		WhisperClientInit:  false,
	}

	if !diag.FFmpegAvailable {
		t.Error("FFmpegAvailable should be true")
	}
	if diag.FFmpegVersion != "5.1.2" {
		t.Errorf("FFmpegVersion = %q, want %q", diag.FFmpegVersion, "5.1.2")
	}
	if diag.WhisperEnabled {
		t.Error("WhisperEnabled should be false")
	}
}

func TestErrAIAlreadyInProgress(t *testing.T) {
	if ErrAIAlreadyInProgress.Error() != "AI analysis already in progress for this tweet" {
		t.Errorf("unexpected error message: %s", ErrAIAlreadyInProgress.Error())
	}
}
