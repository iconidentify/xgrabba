package grok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple string",
			input: "Hello World",
			want:  "hello_world",
		},
		{
			name:  "with quotes",
			input: `"quoted string"`,
			want:  "quoted_string",
		},
		{
			name:  "with special chars",
			input: "file:name/with\\special*chars?",
			want:  "file_name_with_special_chars",
		},
		{
			name:  "multiple spaces",
			input: "multiple   spaces   here",
			want:  "multiple_spaces_here",
		},
		{
			name:  "leading/trailing underscores",
			input: "  _leading_trailing_  ",
			want:  "leading_trailing",
		},
		{
			name:  "newlines",
			input: "line1\nline2\rline3",
			want:  "line1_line2_line3",
		},
		{
			name:  "long string truncation",
			input: "this_is_a_very_long_string_that_exceeds_one_hundred_characters_and_should_be_truncated_to_fit_the_limit_properly",
			want:  "this_is_a_very_long_string_that_exceeds_one_hundred_characters_and_should_be_truncated_to_fit_the_li",
		},
		{
			name:  "already clean",
			input: "clean_filename",
			want:  "clean_filename",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "unicode",
			input: "Hello World",
			want:  "hello_world",
		},
		{
			name:  "pipe character",
			input: "before|after",
			want:  "before_after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsCommonWord(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"the", true},
		{"a", true},
		{"an", true},
		{"is", true},
		{"and", true},
		{"but", true},
		{"elephant", false},
		{"technology", false},
		{"spacex", false},
		{"i", true},
		{"my", true},
		{"what", true},
	}

	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := isCommonWord(tt.word)
			if got != tt.want {
				t.Errorf("isCommonWord(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}

func TestExtractBasicTags(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantMin int
		wantMax int
	}{
		{
			name:    "simple text",
			text:    "SpaceX launches Starship rocket from Texas",
			wantMin: 3,
			wantMax: 10,
		},
		{
			name:    "text with common words",
			text:    "The quick brown fox jumps over the lazy dog",
			wantMin: 2,
			wantMax: 10,
		},
		{
			name:    "empty text",
			text:    "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "only common words",
			text:    "the is are was were",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "punctuated text",
			text:    "Hello, world! How are you? Testing: one, two, three.",
			wantMin: 2,
			wantMax: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := extractBasicTags(tt.text)
			if len(tags) < tt.wantMin {
				t.Errorf("extractBasicTags(%q) returned %d tags, want at least %d", tt.text, len(tags), tt.wantMin)
			}
			if len(tags) > tt.wantMax {
				t.Errorf("extractBasicTags(%q) returned %d tags, want at most %d", tt.text, len(tags), tt.wantMax)
			}
		})
	}
}

func TestFallbackFilename(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		postedAt  time.Time
		tweetText string
		want      string
	}{
		{
			name:      "normal case",
			username:  "elonmusk",
			postedAt:  time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			tweetText: "SpaceX launches Starship from Texas today",
			want:      "elonmusk_2024-01-15_spacex_launches_starship_from_texas",
		},
		{
			name:      "short tweet",
			username:  "nasa",
			postedAt:  time.Date(2024, 3, 20, 15, 30, 0, 0, time.UTC),
			tweetText: "Launch successful",
			want:      "nasa_2024-03-20_launch_successful",
		},
		{
			name:      "empty tweet",
			username:  "testuser",
			postedAt:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			tweetText: "",
			want:      "testuser_2024-06-01_video",
		},
		{
			name:      "special chars in tweet",
			username:  "user123",
			postedAt:  time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC),
			tweetText: "Hello: World! @test #hashtag",
			want:      "user123_2024-12-25_hello_world!_@test_#hashtag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FallbackFilename(tt.username, tt.postedAt, tt.tweetText)
			if got != tt.want {
				t.Errorf("FallbackFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildFilenamePrompt(t *testing.T) {
	req := FilenameRequest{
		TweetText:      "SpaceX Starship test flight",
		AuthorUsername: "elonmusk",
		AuthorName:     "Elon Musk",
		PostedAt:       "2024-01-15",
		Duration:       120,
	}

	prompt := buildFilenamePrompt(req)

	// Check that prompt contains expected elements
	if !containsAll(prompt,
		"@elonmusk",
		"Elon Musk",
		"2024-01-15",
		"120 seconds",
		"SpaceX Starship test flight",
	) {
		t.Errorf("buildFilenamePrompt missing expected content: %s", prompt)
	}
}

func TestBuildContentAnalysisPrompt(t *testing.T) {
	req := ContentAnalysisRequest{
		TweetText:      "Amazing documentary footage",
		AuthorUsername: "historyChannel",
		HasVideo:       true,
		VideoDuration:  300,
	}

	prompt := buildContentAnalysisPrompt(req)

	if !containsAll(prompt,
		"@historyChannel",
		"Amazing documentary footage",
	) {
		t.Errorf("buildContentAnalysisPrompt missing expected content: %s", prompt)
	}

	// Test with images
	req2 := ContentAnalysisRequest{
		TweetText:      "Image test",
		AuthorUsername: "test",
		HasImages:      true,
		ImageCount:     3,
	}
	prompt2 := buildContentAnalysisPrompt(req2)
	if !containsAll(prompt2, "@test", "Image test") {
		t.Errorf("buildContentAnalysisPrompt with images missing expected content: %s", prompt2)
	}
}

func TestNewClient(t *testing.T) {
	cfg := config.GrokConfig{
		APIKey:  "test-api-key",
		Model:   "grok-2",
		BaseURL: "https://test.api.com",
		Timeout: 30 * time.Second,
	}

	client := NewClient(cfg)

	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", client.apiKey, "test-api-key")
	}
	if client.model != "grok-2" {
		t.Errorf("model = %q, want %q", client.model, "grok-2")
	}
	if client.baseURL != "https://test.api.com" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://test.api.com")
	}
}

func TestNewClient_EmptyValues(t *testing.T) {
	cfg := config.GrokConfig{
		APIKey: "test-api-key",
		// Model and BaseURL will be empty
	}

	client := NewClient(cfg)

	// Client should be created even with empty values
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", client.apiKey, "test-api-key")
	}
}

func TestHTTPClient_GenerateFilename_Success(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong Authorization header")
		}

		// Return mock response
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "elonmusk_2024-01-15_starship_launch"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := FilenameRequest{
		TweetText:      "Starship launch successful",
		AuthorUsername: "elonmusk",
		AuthorName:     "Elon Musk",
		PostedAt:       "2024-01-15",
	}

	filename, err := client.GenerateFilename(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateFilename failed: %v", err)
	}
	if filename != "elonmusk_2024-01-15_starship_launch" {
		t.Errorf("filename = %q, want %q", filename, "elonmusk_2024-01-15_starship_launch")
	}
}

func TestHTTPClient_GenerateFilename_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := FilenameRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		AuthorName:     "Test",
		PostedAt:       "2024-01-01",
	}

	_, err := client.GenerateFilename(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestHTTPClient_GenerateFilename_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{}, // Empty choices
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := FilenameRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		AuthorName:     "Test",
		PostedAt:       "2024-01-01",
	}

	_, err := client.GenerateFilename(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestHTTPClient_AnalyzeContent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		analysisJSON := `{"summary":"Test content about technology","tags":["technology","test"],"content_type":"educational","topics":["Technology"]}`
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": analysisJSON}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := ContentAnalysisRequest{
		TweetText:      "Technology test content",
		AuthorUsername: "tech",
		HasVideo:       true,
	}

	result, err := client.AnalyzeContent(context.Background(), req)
	if err != nil {
		t.Fatalf("AnalyzeContent failed: %v", err)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(result.Tags) == 0 {
		t.Error("expected non-empty tags")
	}
}

func TestHTTPClient_AnalyzeContent_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return non-JSON content
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "This is not valid JSON"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := ContentAnalysisRequest{
		TweetText:      "Test content about spacex rockets",
		AuthorUsername: "test",
	}

	// Should fallback to basic extraction rather than error
	result, err := client.AnalyzeContent(context.Background(), req)
	if err != nil {
		t.Fatalf("AnalyzeContent should not error on invalid JSON: %v", err)
	}
	// Should have fallback summary
	if result.Summary == "" {
		t.Error("expected fallback summary")
	}
}

func TestHTTPClient_AnalyzeContent_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		model:      "grok-2",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := ContentAnalysisRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
	}

	_, err := client.AnalyzeContent(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

// Helper function
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestHTTPClient_GenerateFilename_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "test"}},
			},
		})
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := FilenameRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		AuthorName:     "Test",
		PostedAt:       "2024-01-01",
	}

	_, err := client.GenerateFilename(ctx, req)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestHTTPClient_GenerateFilename_NetworkError(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://invalid-domain-that-does-not-exist-12345.com",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	req := FilenameRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		AuthorName:     "Test",
		PostedAt:       "2024-01-01",
	}

	_, err := client.GenerateFilename(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestHTTPClient_GenerateFilename_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := FilenameRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		AuthorName:     "Test",
		PostedAt:       "2024-01-01",
	}

	_, err := client.GenerateFilename(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHTTPClient_AnalyzeContent_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := ContentAnalysisRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
	}

	_, err := client.AnalyzeContent(ctx, req)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestHTTPClient_AnalyzeContent_NetworkError(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://invalid-domain-that-does-not-exist-12345.com",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	req := ContentAnalysisRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
	}

	_, err := client.AnalyzeContent(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestHTTPClient_GenerateEssay_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		essayJSON := `{"title":"Test Essay","essay":"# Test Essay\n\nThis is test content.","word_count":5}`
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": essayJSON}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := EssayRequest{
		Transcript:  "This is a test transcript with some content.",
		ContentType: "documentary",
		Style:       "academic",
	}

	result, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateEssay failed: %v", err)
	}
	if result.Title == "" {
		t.Error("expected non-empty title")
	}
	if result.Essay == "" {
		t.Error("expected non-empty essay")
	}
	if result.WordCount == 0 {
		t.Error("expected non-zero word count")
	}
}

func TestHTTPClient_GenerateEssay_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Test transcript",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestHTTPClient_GenerateEssay_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Test",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestHTTPClient_AnalyzeContentWithVision_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		analysisJSON := `{"summary":"Vision analysis","tags":["image","test"],"content_type":"photo","topics":["Technology"]}`
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": analysisJSON}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.jpg")
	os.WriteFile(imgPath, []byte("fake image data"), 0644)

	req := VisionAnalysisRequest{
		TweetText:      "Test with image",
		AuthorUsername: "test",
		ImagePaths:     []string{imgPath},
	}

	result, err := client.AnalyzeContentWithVision(context.Background(), req)
	if err != nil {
		t.Fatalf("AnalyzeContentWithVision failed: %v", err)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestHTTPClient_AnalyzeContentWithVision_ImageNotFound(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://test.com",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := VisionAnalysisRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		ImagePaths:     []string{"/nonexistent/image.jpg"},
	}

	_, err := client.AnalyzeContentWithVision(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-existent image")
	}
}

func TestHTTPClient_AnalyzeContentWithVision_NetworkError(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://invalid-domain-that-does-not-exist-12345.com",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.jpg")
	os.WriteFile(imgPath, []byte("fake"), 0644)

	req := VisionAnalysisRequest{
		TweetText:      "Test",
		AuthorUsername: "test",
		ImagePaths:     []string{imgPath},
	}

	_, err := client.AnalyzeContentWithVision(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}
