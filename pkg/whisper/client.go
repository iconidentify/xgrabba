package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client interfaces with OpenAI's Whisper API for audio transcription.
type Client interface {
	// Transcribe converts audio to text.
	Transcribe(ctx context.Context, req TranscriptionRequest) (*TranscriptionResponse, error)
	// TranscribeFile is a convenience method that takes a file path.
	TranscribeFile(ctx context.Context, audioPath string, opts TranscriptionOptions) (*TranscriptionResponse, error)
}

// TranscriptionRequest contains the audio data and options for transcription.
type TranscriptionRequest struct {
	AudioData   io.Reader
	Filename    string
	Model       string // "whisper-1" or "gpt-4o-transcribe" or "gpt-4o-mini-transcribe"
	Language    string // Optional: ISO-639-1 language code (e.g., "en")
	Prompt      string // Optional: context/prompt to guide transcription
	Temperature float64
}

// TranscriptionOptions for convenience methods.
type TranscriptionOptions struct {
	Model       string
	Language    string
	Prompt      string
	Temperature float64
}

// TranscriptionResponse contains the transcription result.
type TranscriptionResponse struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// TranscriptionSegment represents a segment of the transcription with timing.
type TranscriptionSegment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// HTTPClient implements Client using the OpenAI API.
type HTTPClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// Config for creating a new Whisper client.
type Config struct {
	APIKey  string
	BaseURL string        // Optional, defaults to OpenAI API
	Model   string        // Optional, defaults to "whisper-1"
	Timeout time.Duration // Optional, defaults to 5 minutes
}

// NewClient creates a new OpenAI Whisper client.
func NewClient(cfg Config) *HTTPClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "whisper-1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}

	return &HTTPClient{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Transcribe sends audio to the Whisper API and returns the transcription.
func (c *HTTPClient) Transcribe(ctx context.Context, req TranscriptionRequest) (*TranscriptionResponse, error) {
	if req.Model == "" {
		req.Model = c.model
	}

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add audio file
	part, err := writer.CreateFormFile("file", req.Filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, req.AudioData); err != nil {
		return nil, fmt.Errorf("copy audio data: %w", err)
	}

	// Add model
	if err := writer.WriteField("model", req.Model); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}

	// Add optional fields
	if req.Language != "" {
		if err := writer.WriteField("language", req.Language); err != nil {
			return nil, fmt.Errorf("write language field: %w", err)
		}
	}

	if req.Prompt != "" {
		if err := writer.WriteField("prompt", req.Prompt); err != nil {
			return nil, fmt.Errorf("write prompt field: %w", err)
		}
	}

	if req.Temperature > 0 {
		if err := writer.WriteField("temperature", fmt.Sprintf("%.2f", req.Temperature)); err != nil {
			return nil, fmt.Errorf("write temperature field: %w", err)
		}
	}

	// Request verbose JSON for segment timing
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("write response_format field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close writer: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result TranscriptionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try parsing as simple text response
		var simpleResult struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(respBody, &simpleResult); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		result.Text = simpleResult.Text
	}

	return &result, nil
}

// TranscribeFile transcribes an audio file from disk.
func (c *HTTPClient) TranscribeFile(ctx context.Context, audioPath string, opts TranscriptionOptions) (*TranscriptionResponse, error) {
	file, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("open audio file: %w", err)
	}
	defer file.Close()

	return c.Transcribe(ctx, TranscriptionRequest{
		AudioData:   file,
		Filename:    filepath.Base(audioPath),
		Model:       opts.Model,
		Language:    opts.Language,
		Prompt:      opts.Prompt,
		Temperature: opts.Temperature,
	})
}

// TranscribeChunks transcribes multiple audio chunks and combines the results.
// This is useful for files larger than the 25MB API limit.
func (c *HTTPClient) TranscribeChunks(ctx context.Context, chunkPaths []string, opts TranscriptionOptions) (*TranscriptionResponse, error) {
	if len(chunkPaths) == 0 {
		return nil, fmt.Errorf("no chunks provided")
	}

	if len(chunkPaths) == 1 {
		return c.TranscribeFile(ctx, chunkPaths[0], opts)
	}

	var allText strings.Builder
	var allSegments []TranscriptionSegment
	var totalDuration float64
	var lastSegmentEnd float64

	for i, chunkPath := range chunkPaths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		result, err := c.TranscribeFile(ctx, chunkPath, opts)
		if err != nil {
			// IMPORTANT: don't silently drop chunks; that yields truncated transcripts.
			return nil, fmt.Errorf("transcribe chunk %d/%d (%s): %w", i+1, len(chunkPaths), chunkPath, err)
		}

		// Add text with space separator
		if allText.Len() > 0 {
			allText.WriteString(" ")
		}
		allText.WriteString(strings.TrimSpace(result.Text))

		// Adjust segment timings for concatenation
		for _, seg := range result.Segments {
			adjustedSeg := seg
			adjustedSeg.Start += lastSegmentEnd
			adjustedSeg.End += lastSegmentEnd
			adjustedSeg.ID = len(allSegments)
			allSegments = append(allSegments, adjustedSeg)
		}

		// Update timing offset for next chunk
		if result.Duration > 0 {
			lastSegmentEnd += result.Duration
			totalDuration += result.Duration
		} else if len(result.Segments) > 0 {
			// Estimate from last segment
			lastSeg := result.Segments[len(result.Segments)-1]
			lastSegmentEnd += lastSeg.End
			totalDuration += lastSeg.End
		} else {
			// Estimate 5 minutes per chunk
			lastSegmentEnd += 300
			totalDuration += 300
		}

		// Use previous chunk's text as prompt for continuity
		if i < len(chunkPaths)-1 && result.Text != "" {
			words := strings.Fields(result.Text)
			if len(words) > 20 {
				opts.Prompt = strings.Join(words[len(words)-20:], " ")
			} else {
				opts.Prompt = result.Text
			}
		}
	}

	return &TranscriptionResponse{
		Text:     allText.String(),
		Duration: totalDuration,
		Segments: allSegments,
	}, nil
}

// EstimateCost estimates the transcription cost in USD.
// Based on OpenAI pricing: $0.006/minute for whisper-1
func EstimateCost(durationSeconds float64, model string) float64 {
	minutes := durationSeconds / 60.0
	switch model {
	case "gpt-4o-mini-transcribe":
		return minutes * 0.003
	case "whisper-1", "gpt-4o-transcribe":
		return minutes * 0.006
	default:
		return minutes * 0.006
	}
}

// SupportedFormats returns the audio formats supported by Whisper.
func SupportedFormats() []string {
	return []string{
		"flac", "m4a", "mp3", "mp4", "mpeg", "mpga", "oga", "ogg", "wav", "webm",
	}
}

// IsSupportedFormat checks if a file format is supported.
func IsSupportedFormat(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	for _, format := range SupportedFormats() {
		if ext == format {
			return true
		}
	}
	return false
}

// MaxFileSize is the maximum file size for the Whisper API (25MB).
const MaxFileSize = 25 * 1024 * 1024
