package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chrisk/xgrabba/internal/config"
)

// Client interfaces with Grok AI for filename generation.
type Client interface {
	// GenerateFilename creates a descriptive filename based on video metadata.
	GenerateFilename(ctx context.Context, req FilenameRequest) (string, error)
}

// FilenameRequest contains information for generating a filename.
type FilenameRequest struct {
	TweetText      string
	AuthorUsername string
	AuthorName     string
	PostedAt       string
	Duration       int
}

// HTTPClient implements Client using HTTP requests to the Grok API.
type HTTPClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewClient creates a new Grok API client.
func NewClient(cfg config.GrokConfig) *HTTPClient {
	return &HTTPClient{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// chatRequest is the request body for the Grok chat API.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the response from the Grok chat API.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// GenerateFilename creates a descriptive filename based on video metadata.
func (c *HTTPClient) GenerateFilename(ctx context.Context, req FilenameRequest) (string, error) {
	prompt := buildFilenamePrompt(req)

	chatReq := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "You are a helpful assistant that generates concise, descriptive filenames for archived videos. Return ONLY the filename without any extension, explanation, or surrounding text. Use underscores instead of spaces. Keep filenames under 50 characters.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response from Grok")
	}

	filename := sanitizeFilename(chatResp.Choices[0].Message.Content)
	return filename, nil
}

func buildFilenamePrompt(req FilenameRequest) string {
	var sb strings.Builder
	sb.WriteString("Generate a concise, descriptive filename for this archived video:\n\n")
	sb.WriteString(fmt.Sprintf("Author: @%s (%s)\n", req.AuthorUsername, req.AuthorName))
	sb.WriteString(fmt.Sprintf("Date: %s\n", req.PostedAt))

	if req.Duration > 0 {
		sb.WriteString(fmt.Sprintf("Duration: %d seconds\n", req.Duration))
	}

	sb.WriteString(fmt.Sprintf("Tweet text: \"%s\"\n\n", req.TweetText))
	sb.WriteString("If this appears to be from a known documentary, show, movie, or media source, include that context in the filename.\n")
	sb.WriteString("Format: author_date_description (e.g., elonmusk_2024-01-15_starship_launch_test)\n")
	sb.WriteString("Return ONLY the filename, no extension, no quotes, no explanation.")

	return sb.String()
}

func sanitizeFilename(s string) string {
	// Trim whitespace and quotes
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")

	// Remove or replace invalid filename characters
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r"}
	for _, char := range invalid {
		s = strings.ReplaceAll(s, char, "_")
	}

	// Replace spaces with underscores
	s = strings.ReplaceAll(s, " ", "_")

	// Remove consecutive underscores
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}

	// Trim underscores from ends
	s = strings.Trim(s, "_")

	// Convert to lowercase
	s = strings.ToLower(s)

	// Limit length
	if len(s) > 100 {
		s = s[:100]
	}

	return s
}

// FallbackFilename generates a simple filename when Grok is unavailable.
func FallbackFilename(username string, postedAt time.Time, tweetText string) string {
	date := postedAt.Format("2006-01-02")

	// Extract first few words from tweet
	words := strings.Fields(tweetText)
	var description string
	if len(words) > 5 {
		description = strings.Join(words[:5], "_")
	} else if len(words) > 0 {
		description = strings.Join(words, "_")
	} else {
		description = "video"
	}

	filename := fmt.Sprintf("%s_%s_%s", username, date, description)
	return sanitizeFilename(filename)
}
