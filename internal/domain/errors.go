package domain

import "errors"

// Domain errors.
var (
	// ErrVideoNotFound is returned when a video cannot be found.
	ErrVideoNotFound = errors.New("video not found")

	// ErrJobNotFound is returned when a job cannot be found.
	ErrJobNotFound = errors.New("job not found")

	// ErrNoJobs is returned when there are no jobs to process.
	ErrNoJobs = errors.New("no jobs available")

	// ErrDuplicateVideo is returned when attempting to archive a video that already exists.
	ErrDuplicateVideo = errors.New("video already archived")

	// ErrInvalidTweetURL is returned when the tweet URL is invalid.
	ErrInvalidTweetURL = errors.New("invalid tweet URL")

	// ErrNoMediaURLs is returned when no media URLs are provided.
	ErrNoMediaURLs = errors.New("no media URLs provided")

	// ErrDownloadFailed is returned when the video download fails.
	ErrDownloadFailed = errors.New("video download failed")

	// ErrDownloadTimeout is returned when the download times out.
	ErrDownloadTimeout = errors.New("video download timed out")

	// ErrURLExpired is returned when the video URL has expired.
	ErrURLExpired = errors.New("video URL has expired")

	// ErrStorageFull is returned when there is insufficient storage space.
	ErrStorageFull = errors.New("insufficient storage space")

	// ErrGrokAPIFailed is returned when the Grok API call fails.
	ErrGrokAPIFailed = errors.New("grok API call failed")

	// ErrInvalidAPIKey is returned when the API key is invalid.
	ErrInvalidAPIKey = errors.New("invalid API key")

	// ErrRateLimited is returned when rate limited by external services.
	ErrRateLimited = errors.New("rate limited")

	// ErrMediaNotFound is returned when a media file cannot be found.
	ErrMediaNotFound = errors.New("media file not found")

	// ErrPlaylistNotFound is returned when a playlist cannot be found.
	ErrPlaylistNotFound = errors.New("playlist not found")

	// ErrDuplicatePlaylist is returned when a playlist with the same name already exists.
	ErrDuplicatePlaylist = errors.New("playlist with this name already exists")

	// ErrEmptyPlaylistName is returned when a playlist name is empty.
	ErrEmptyPlaylistName = errors.New("playlist name cannot be empty")

	// ErrTweetNotInPlaylist is returned when trying to remove a tweet that's not in the playlist.
	ErrTweetNotInPlaylist = errors.New("tweet not in playlist")

	// ErrSmartPlaylistNoManualItems is returned when trying to manually add/remove items from a smart playlist.
	ErrSmartPlaylistNoManualItems = errors.New("cannot manually modify items in a smart playlist")

	// ErrSmartPlaylistNoReorder is returned when trying to reorder items in a smart playlist.
	ErrSmartPlaylistNoReorder = errors.New("cannot reorder a smart playlist")

	// ErrEmptySmartQuery is returned when a smart playlist is created with an empty query.
	ErrEmptySmartQuery = errors.New("smart playlist query cannot be empty")
)

// VideoError wraps an error with video context.
type VideoError struct {
	VideoID VideoID
	Op      string
	Err     error
}

func (e *VideoError) Error() string {
	if e.VideoID != "" {
		return e.Op + " [" + e.VideoID.String() + "]: " + e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *VideoError) Unwrap() error {
	return e.Err
}

// NewVideoError creates a new VideoError.
func NewVideoError(videoID VideoID, op string, err error) *VideoError {
	return &VideoError{
		VideoID: videoID,
		Op:      op,
		Err:     err,
	}
}
