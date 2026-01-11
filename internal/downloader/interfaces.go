package downloader

import (
	"context"
	"io"
)

// Downloader fetches video content from URLs.
type Downloader interface {
	// Download fetches video from URL, returns content reader, size, and content type.
	// Caller is responsible for closing the reader.
	Download(ctx context.Context, url string) (io.ReadCloser, int64, error)

	// Probe checks URL accessibility without downloading full content.
	Probe(ctx context.Context, url string) (*ProbeResult, error)
}

// ProbeResult contains information about a video URL.
type ProbeResult struct {
	ContentType   string
	ContentLength int64
	Accessible    bool
	Error         string
}
