package grok

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
)

// Client interfaces with Grok AI for filename generation and content analysis.
type Client interface {
	// GenerateFilename creates a descriptive filename based on video metadata.
	GenerateFilename(ctx context.Context, req FilenameRequest) (string, error)
	// AnalyzeContent generates searchable tags and description for media content.
	AnalyzeContent(ctx context.Context, req ContentAnalysisRequest) (*ContentAnalysisResponse, error)
	// AnalyzeContentWithVision analyzes content including actual images for rich metadata.
	AnalyzeContentWithVision(ctx context.Context, req VisionAnalysisRequest) (*ContentAnalysisResponse, error)
	// GenerateEssay creates a high-quality markdown essay from a video transcript.
	GenerateEssay(ctx context.Context, req EssayRequest) (*EssayResponse, error)
}

// ContentAnalysisRequest contains information for analyzing tweet content.
type ContentAnalysisRequest struct {
	TweetText      string
	AuthorUsername string
	HasVideo       bool
	HasImages      bool
	ImageCount     int
	VideoDuration  int // seconds
}

// VisionAnalysisRequest contains information for vision-based content analysis.
type VisionAnalysisRequest struct {
	TweetText      string
	AuthorUsername string
	ImagePaths     []string // Local file paths to images
	VideoThumbPath string   // Video thumbnail path for video analysis
	HasVideo       bool
	VideoDuration  int
}

// ContentAnalysisResponse contains AI-generated analysis of tweet content.
type ContentAnalysisResponse struct {
	Summary     string   // Brief description of what the content is about
	Tags        []string // Searchable keywords/tags
	ContentType string   // e.g., "documentary", "news", "comedy", "sports", etc.
	Topics      []string // Main topics discussed or shown
}

// EssayRequest contains information for generating an essay from a transcript.
type EssayRequest struct {
	Transcript  string // Full video transcript
	ContentType string // Detected content type (documentary, lecture, etc.) - optional hint
}

// EssayResponse contains the generated essay.
type EssayResponse struct {
	Title     string // Essay title
	Essay     string // Full markdown essay
	WordCount int    // Word count of the essay
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
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []contentPart for vision
}

// contentPart represents a part of multimodal content (text or image).
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low", "high", or "auto"
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

// AnalyzeContent generates searchable tags and description for media content.
func (c *HTTPClient) AnalyzeContent(ctx context.Context, req ContentAnalysisRequest) (*ContentAnalysisResponse, error) {
	prompt := buildContentAnalysisPrompt(req)

	chatReq := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: `You are a content analyzer that extracts searchable metadata from tweets.
Return your analysis as JSON with these fields:
- summary: 1-2 sentence description of what the content shows/discusses
- tags: array of 5-15 searchable keywords (people, places, objects, events, concepts)
- content_type: category like "documentary", "news", "comedy", "sports", "music", "politics", "science", "tutorial", "meme", "personal", "promotional"
- topics: array of 2-5 main topics

Example output:
{"summary":"Historical footage of World War 2 showing tank battles in North Africa","tags":["ww2","world war 2","tanks","north africa","rommel","desert fox","history","military","1942"],"content_type":"documentary","topics":["World War 2","Military History","North Africa Campaign"]}

Return ONLY valid JSON, no markdown, no explanation.`,
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from Grok")
	}

	// Parse the JSON response
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Clean up potential markdown code blocks
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result ContentAnalysisResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// If JSON parsing fails, create a basic response from the text
		return &ContentAnalysisResponse{
			Summary: content,
			Tags:    extractBasicTags(req.TweetText),
		}, nil
	}

	return &result, nil
}

// AnalyzeContentWithVision analyzes content including actual images for rich metadata extraction.
// This sends images to the vision model to extract text, objects, people, and context from the actual media.
func (c *HTTPClient) AnalyzeContentWithVision(ctx context.Context, req VisionAnalysisRequest) (*ContentAnalysisResponse, error) {
	// Build multimodal content parts
	var contentParts []contentPart

	// Add text prompt first
	promptText := buildVisionPrompt(req)
	contentParts = append(contentParts, contentPart{
		Type: "text",
		Text: promptText,
	})

	// Add images (up to 4 to stay within limits)
	imagePaths := req.ImagePaths
	if req.VideoThumbPath != "" {
		// For videos, use the thumbnail
		imagePaths = append([]string{req.VideoThumbPath}, imagePaths...)
	}

	maxImages := 4
	if len(imagePaths) > maxImages {
		imagePaths = imagePaths[:maxImages]
	}

	for _, imgPath := range imagePaths {
		base64Data, mimeType, err := encodeImageToBase64(imgPath)
		if err != nil {
			// Skip images that can't be read, but continue with others
			continue
		}

		contentParts = append(contentParts, contentPart{
			Type: "image_url",
			ImageURL: &imageURL{
				URL:    fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data),
				Detail: "high",
			},
		})
	}

	// If no images could be loaded, fall back to text-only analysis
	if len(contentParts) == 1 {
		return c.AnalyzeContent(ctx, ContentAnalysisRequest{
			TweetText:      req.TweetText,
			AuthorUsername: req.AuthorUsername,
			HasVideo:       req.HasVideo,
			HasImages:      len(req.ImagePaths) > 0,
			ImageCount:     len(req.ImagePaths),
			VideoDuration:  req.VideoDuration,
		})
	}

	// Use vision model (grok-2-vision or similar)
	visionModel := c.model
	if !strings.Contains(visionModel, "vision") {
		visionModel = "grok-2-vision-1212" // Default vision model
	}

	chatReq := chatRequest{
		Model: visionModel,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: `You are an expert content analyzer with vision capabilities. Analyze the provided images/video thumbnail along with the tweet text.

IMPORTANT: Extract EVERYTHING visible in the images:
- All text, captions, memes text, watermarks, titles
- People (identify if recognizable public figures)
- Objects, brands, logos
- Locations, landmarks
- Historical context, time periods
- Cultural references, symbols
- Actions, events happening

Return your analysis as JSON with these fields:
- summary: 2-3 sentence detailed description of what the content shows, including any text visible in images
- tags: array of 15-30 searchable keywords (extract ALL relevant terms - people, places, objects, text from images, events, concepts, brands)
- content_type: category like "meme", "documentary", "news", "comedy", "sports", "music", "politics", "science", "tutorial", "personal", "promotional", "historical"
- topics: array of 3-7 main topics

Be thorough - if there's text in a meme about the "Talmud", include "talmud" in tags. If there's a brand logo, include the brand. If there's a historical figure, include their name.

Return ONLY valid JSON, no markdown, no explanation.`,
			},
			{
				Role:    "user",
				Content: contentParts,
			},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from Grok")
	}

	// Parse the JSON response
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result ContentAnalysisResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return &ContentAnalysisResponse{
			Summary: content,
			Tags:    extractBasicTags(req.TweetText),
		}, nil
	}

	return &result, nil
}

// encodeImageToBase64 reads an image file and returns base64 encoded data with MIME type.
func encodeImageToBase64(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read image: %w", err)
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(path))
	var mimeType string
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	default:
		mimeType = "image/jpeg" // Default to JPEG
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return encoded, mimeType, nil
}

func buildVisionPrompt(req VisionAnalysisRequest) string {
	var sb strings.Builder
	sb.WriteString("Analyze this tweet and its media content. Extract ALL searchable metadata:\n\n")
	sb.WriteString(fmt.Sprintf("Author: @%s\n", req.AuthorUsername))

	if req.TweetText != "" {
		sb.WriteString(fmt.Sprintf("Tweet text: \"%s\"\n", req.TweetText))
	} else {
		sb.WriteString("Tweet text: (no text, media only)\n")
	}

	if req.HasVideo {
		sb.WriteString(fmt.Sprintf("Media type: Video (%d seconds) - thumbnail provided\n", req.VideoDuration))
	} else if len(req.ImagePaths) > 0 {
		sb.WriteString(fmt.Sprintf("Media type: %d images\n", len(req.ImagePaths)))
	}

	sb.WriteString("\nCarefully examine the image(s) and extract:")
	sb.WriteString("\n- Any text visible (memes, captions, signs, watermarks)")
	sb.WriteString("\n- People (especially public figures)")
	sb.WriteString("\n- Objects, products, brands")
	sb.WriteString("\n- Locations, settings")
	sb.WriteString("\n- Historical or cultural context")
	sb.WriteString("\n- The overall topic and meaning")

	return sb.String()
}

func buildContentAnalysisPrompt(req ContentAnalysisRequest) string {
	var sb strings.Builder
	sb.WriteString("Analyze this tweet and extract searchable metadata:\n\n")
	sb.WriteString(fmt.Sprintf("Author: @%s\n", req.AuthorUsername))

	if req.TweetText != "" {
		sb.WriteString(fmt.Sprintf("Tweet text: \"%s\"\n", req.TweetText))
	} else {
		sb.WriteString("Tweet text: (no text, media only)\n")
	}

	if req.HasVideo {
		sb.WriteString(fmt.Sprintf("Media: Video (%d seconds)\n", req.VideoDuration))
	} else if req.HasImages {
		sb.WriteString(fmt.Sprintf("Media: %d images\n", req.ImageCount))
	}

	sb.WriteString("\nBased on the author, text, and media type, infer what this content likely shows or discusses. ")
	sb.WriteString("If it's from a known account (news, documentary, sports, etc.), use that context. ")
	sb.WriteString("Generate comprehensive tags that someone might search for to find this content.")

	return sb.String()
}

func extractBasicTags(text string) []string {
	// Simple fallback: extract words that could be tags
	words := strings.Fields(strings.ToLower(text))
	var tags []string
	seen := make(map[string]bool)

	for _, word := range words {
		// Clean the word
		word = strings.Trim(word, ".,!?\"'()[]{}:;")
		if len(word) < 3 || len(word) > 30 {
			continue
		}
		// Skip common words
		if isCommonWord(word) {
			continue
		}
		if !seen[word] {
			seen[word] = true
			tags = append(tags, word)
		}
		if len(tags) >= 10 {
			break
		}
	}
	return tags
}

func isCommonWord(word string) bool {
	common := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true, "can": true,
		"and": true, "but": true, "or": true, "nor": true, "for": true,
		"yet": true, "so": true, "at": true, "by": true, "from": true,
		"in": true, "into": true, "of": true, "on": true, "to": true,
		"with": true, "this": true, "that": true, "these": true, "those": true,
		"it": true, "its": true, "you": true, "your": true, "we": true,
		"our": true, "they": true, "their": true, "he": true, "she": true,
		"his": true, "her": true, "i": true, "me": true, "my": true,
		"just": true, "like": true, "get": true, "got": true, "all": true,
		"when": true, "what": true, "who": true, "how": true, "why": true,
		"where": true, "which": true, "there": true, "here": true, "not": true,
	}
	return common[word]
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

// GenerateEssay creates a high-quality markdown essay from a video transcript.
// The essay is presented as an original standalone piece - no references to the source video or transcript.
func (c *HTTPClient) GenerateEssay(ctx context.Context, req EssayRequest) (*EssayResponse, error) {
	if req.Transcript == "" {
		return nil, fmt.Errorf("transcript is required for essay generation")
	}

	// Use the best available model for essay generation
	model := c.model
	if !strings.Contains(model, "vision") {
		// For essay generation, we want the most capable text model
		model = "grok-3" // Use Grok 3 for highest quality essays
	}

	systemPrompt := `You are an expert academic writer tasked with transforming a raw transcript into a cohesive, original essay.

CRITICAL REQUIREMENTS:
1. Transform the transcript into a well-structured essay with introduction, body paragraphs, and conclusion
2. Faithfully cover ALL key points, arguments, facts, and narratives from the transcript
3. Use formal language, smooth transitions, and engaging prose suitable for a general audience
4. NEVER mention or reference the transcript, video, source, or the fact this is derived from any source
5. Present it as an original standalone essay on the topic
6. Do NOT inject any personal opinions, commentary, or value judgments about the content
7. Report the information objectively and neutrally, even if the content is controversial
8. Let the facts and arguments speak for themselves without editorial framing

OUTPUT FORMAT:
Return your response as JSON with exactly two fields:
{
  "title": "An Appropriate Essay Title Based on the Content",
  "essay": "The full markdown essay content here..."
}

ESSAY FORMATTING:
- Use markdown for structure (## for section headings, **bold** for emphasis)
- Write in clear paragraphs with logical flow
- Include smooth transitions between sections
- Aim for comprehensive coverage - the essay should be thorough
- Keep the title concise but descriptive (5-12 words typically)

Return ONLY valid JSON, no markdown code blocks, no explanation.`

	userPrompt := fmt.Sprintf("Transform this transcript into a polished essay:\n\n%s", req.Transcript)

	chatReq := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from Grok")
	}

	// Parse the JSON response
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Clean up potential markdown code blocks
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Title string `json:"title"`
		Essay string `json:"essay"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// If JSON parsing fails, try to extract content directly
		// This handles cases where the model returns plain text
		return &EssayResponse{
			Title:     "Essay",
			Essay:     content,
			WordCount: len(strings.Fields(content)),
		}, nil
	}

	return &EssayResponse{
		Title:     result.Title,
		Essay:     result.Essay,
		WordCount: len(strings.Fields(result.Essay)),
	}, nil
}
