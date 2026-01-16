package grok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateEssay_Success(t *testing.T) {
	// Create mock server
	mockResponse := map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"content": `{"title": "The History of Computing", "essay": "## Introduction\n\nComputing has transformed our world...\n\n## The Early Days\n\nIn the beginning, computers were massive machines..."}`,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify authorization header
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("missing or invalid authorization header")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-api-key",
		model:      "grok-2-vision-1212",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript:  "Today we're going to discuss the history of computing. In the early days, computers were massive machines that filled entire rooms...",
		ContentType: "documentary",
	}

	resp, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateEssay failed: %v", err)
	}

	if resp.Title != "The History of Computing" {
		t.Errorf("expected title 'The History of Computing', got '%s'", resp.Title)
	}

	if !strings.Contains(resp.Essay, "Introduction") {
		t.Errorf("expected essay to contain 'Introduction', got: %s", resp.Essay)
	}

	if resp.WordCount == 0 {
		t.Error("expected non-zero word count")
	}
}

func TestGenerateEssay_EmptyTranscript(t *testing.T) {
	client := &HTTPClient{
		baseURL:    "http://example.com",
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{},
	}

	req := EssayRequest{
		Transcript: "",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Error("expected error for empty transcript")
	}
	if !strings.Contains(err.Error(), "transcript is required") {
		t.Errorf("expected transcript required error, got: %v", err)
	}
}

func TestGenerateEssay_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "Internal server error"}}`))
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript content",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Error("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "API error") {
		t.Errorf("expected API error, got: %v", err)
	}
}

func TestGenerateEssay_MalformedJSON(t *testing.T) {
	// Test handling of non-JSON response from Grok
	mockResponse := map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"content": "This is just plain text, not JSON. It's an essay about history.",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript",
	}

	// Should fallback gracefully
	resp, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("expected fallback handling, got error: %v", err)
	}

	// Should use fallback title and use content as essay
	if resp.Title != "Essay" {
		t.Errorf("expected fallback title 'Essay', got '%s'", resp.Title)
	}
	if resp.Essay == "" {
		t.Error("expected essay content from fallback")
	}
}

func TestGenerateEssay_MarkdownCodeBlockCleanup(t *testing.T) {
	// Test that markdown code blocks are cleaned up
	mockResponse := map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"content": "```json\n{\"title\": \"Test Essay\", \"essay\": \"Content here\"}\n```",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript",
	}

	resp, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("expected successful parsing, got error: %v", err)
	}

	if resp.Title != "Test Essay" {
		t.Errorf("expected title 'Test Essay', got '%s'", resp.Title)
	}
}

func TestGenerateEssay_LongTranscript(t *testing.T) {
	// Test that long transcripts are handled properly
	longTranscript := strings.Repeat("This is a sample sentence from the transcript. ", 500)

	var receivedContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		// Extract the user message content
		if messages, ok := req["messages"].([]interface{}); ok {
			for _, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					if m["role"] == "user" {
						if content, ok := m["content"].(string); ok {
							receivedContent = content
						}
					}
				}
			}
		}

		mockResponse := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": `{"title": "Long Essay", "essay": "This is a long essay..."}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: longTranscript,
	}

	resp, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success with long transcript, got error: %v", err)
	}

	if resp.Title == "" {
		t.Error("expected non-empty title")
	}

	// Verify the full transcript was sent
	if !strings.Contains(receivedContent, longTranscript) {
		t.Error("expected full transcript to be sent to API")
	}
}

func TestEssayRequest_Fields(t *testing.T) {
	req := EssayRequest{
		Transcript:  "test transcript",
		ContentType: "documentary",
	}

	if req.Transcript != "test transcript" {
		t.Errorf("unexpected transcript: %s", req.Transcript)
	}
	if req.ContentType != "documentary" {
		t.Errorf("unexpected content type: %s", req.ContentType)
	}
}

func TestEssayResponse_Fields(t *testing.T) {
	resp := EssayResponse{
		Title:     "Test Title",
		Essay:     "Test essay content",
		WordCount: 100,
	}

	if resp.Title != "Test Title" {
		t.Errorf("unexpected title: %s", resp.Title)
	}
	if resp.Essay != "Test essay content" {
		t.Errorf("unexpected essay: %s", resp.Essay)
	}
	if resp.WordCount != 100 {
		t.Errorf("unexpected word count: %d", resp.WordCount)
	}
}

func TestGenerateEssay_NoChoices(t *testing.T) {
	// Test handling when API returns no choices
	mockResponse := map[string]interface{}{
		"choices": []map[string]interface{}{},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Error("expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no response") {
		t.Errorf("expected 'no response' error, got: %v", err)
	}
}

func TestGenerateEssay_APIErrorResponse(t *testing.T) {
	// Test handling when API returns error object
	mockResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"message": "Rate limit exceeded",
			"type":    "rate_limit_error",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript",
	}

	_, err := client.GenerateEssay(context.Background(), req)
	if err == nil {
		t.Error("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "Rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestGenerateEssay_WordCountCalculation(t *testing.T) {
	// Test that word count is calculated correctly
	mockResponse := map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"content": `{"title": "Test", "essay": "one two three four five six seven eight nine ten"}`,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := &HTTPClient{
		baseURL:    server.URL,
		apiKey:     "test-key",
		model:      "grok-2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	req := EssayRequest{
		Transcript: "Some transcript",
	}

	resp, err := client.GenerateEssay(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.WordCount != 10 {
		t.Errorf("expected word count 10, got %d", resp.WordCount)
	}
}
