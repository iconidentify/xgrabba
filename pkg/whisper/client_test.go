package whisper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cfg := Config{
		APIKey:  "test-api-key",
		BaseURL: "https://custom.api.com/v1",
		Model:   "whisper-1",
		Timeout: 10 * time.Minute,
	}

	client := NewClient(cfg)

	if client == nil {
		t.Fatal("client should not be nil")
	}
	if client.apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", client.apiKey, "test-api-key")
	}
	if client.baseURL != "https://custom.api.com/v1" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://custom.api.com/v1")
	}
	if client.model != "whisper-1" {
		t.Errorf("model = %q, want %q", client.model, "whisper-1")
	}
}

func TestNewClient_Defaults(t *testing.T) {
	cfg := Config{
		APIKey: "test-api-key",
		// BaseURL, Model, Timeout not specified
	}

	client := NewClient(cfg)

	if client.baseURL != "https://api.openai.com/v1" {
		t.Errorf("default baseURL = %q, want %q", client.baseURL, "https://api.openai.com/v1")
	}
	if client.model != "whisper-1" {
		t.Errorf("default model = %q, want %q", client.model, "whisper-1")
	}
}

func TestHTTPClient_Transcribe_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "transcriptions") {
			t.Errorf("expected path to contain 'transcriptions', got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong Authorization header")
		}

		// Return mock response
		resp := TranscriptionResponse{
			Text:     "This is a test transcription.",
			Language: "en",
			Duration: 10.5,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio data"),
		Filename:  "test.mp3",
	}

	resp, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	if resp.Text != "This is a test transcription." {
		t.Errorf("Text = %q, want %q", resp.Text, "This is a test transcription.")
	}
	if resp.Language != "en" {
		t.Errorf("Language = %q, want %q", resp.Language, "en")
	}
}

func TestHTTPClient_Transcribe_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Invalid audio format",
			},
		})
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio data"),
		Filename:  "test.mp3",
	}

	_, err := client.Transcribe(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestHTTPClient_Transcribe_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio data"),
		Filename:  "test.mp3",
	}

	_, err := client.Transcribe(ctx, req)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestHTTPClient_TranscribeFile(t *testing.T) {
	// Create a temporary audio file
	tmpDir := t.TempDir()
	audioPath := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(audioPath, []byte("fake audio data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := TranscriptionResponse{
			Text:     "Transcribed from file",
			Language: "en",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	opts := TranscriptionOptions{
		Language: "en",
	}

	resp, err := client.TranscribeFile(context.Background(), audioPath, opts)
	if err != nil {
		t.Fatalf("TranscribeFile failed: %v", err)
	}

	if resp.Text != "Transcribed from file" {
		t.Errorf("Text = %q, want %q", resp.Text, "Transcribed from file")
	}
}

func TestHTTPClient_TranscribeFile_NotFound(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "https://example.com",
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	_, err := client.TranscribeFile(context.Background(), "/nonexistent/file.mp3", TranscriptionOptions{})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTranscriptionRequest(t *testing.T) {
	req := TranscriptionRequest{
		AudioData:   strings.NewReader("audio"),
		Filename:    "test.mp3",
		Model:       "whisper-1",
		Language:    "en",
		Prompt:      "Test prompt",
		Temperature: 0.5,
	}

	if req.Filename != "test.mp3" {
		t.Errorf("Filename = %q, want %q", req.Filename, "test.mp3")
	}
	if req.Model != "whisper-1" {
		t.Errorf("Model = %q, want %q", req.Model, "whisper-1")
	}
	if req.Temperature != 0.5 {
		t.Errorf("Temperature = %f, want 0.5", req.Temperature)
	}
}

func TestTranscriptionOptions(t *testing.T) {
	opts := TranscriptionOptions{
		Model:       "gpt-4o-transcribe",
		Language:    "es",
		Prompt:      "Spanish audio",
		Temperature: 0.2,
	}

	if opts.Model != "gpt-4o-transcribe" {
		t.Errorf("Model = %q, want %q", opts.Model, "gpt-4o-transcribe")
	}
	if opts.Language != "es" {
		t.Errorf("Language = %q, want %q", opts.Language, "es")
	}
}

func TestTranscriptionResponse(t *testing.T) {
	resp := TranscriptionResponse{
		Text:     "Test transcription",
		Language: "en",
		Duration: 15.5,
		Segments: []TranscriptionSegment{
			{ID: 0, Start: 0.0, End: 5.0, Text: "First segment"},
			{ID: 1, Start: 5.0, End: 10.0, Text: "Second segment"},
		},
	}

	if resp.Text != "Test transcription" {
		t.Errorf("Text = %q, want %q", resp.Text, "Test transcription")
	}
	if len(resp.Segments) != 2 {
		t.Errorf("Segments count = %d, want 2", len(resp.Segments))
	}
}

func TestTranscriptionSegment(t *testing.T) {
	segment := TranscriptionSegment{
		ID:    0,
		Start: 1.5,
		End:   3.5,
		Text:  "Hello world",
	}

	if segment.ID != 0 {
		t.Errorf("ID = %d, want 0", segment.ID)
	}
	if segment.Start != 1.5 {
		t.Errorf("Start = %f, want 1.5", segment.Start)
	}
	if segment.End != 3.5 {
		t.Errorf("End = %f, want 3.5", segment.End)
	}
}

func TestConfig(t *testing.T) {
	cfg := Config{
		APIKey:  "sk-test",
		BaseURL: "https://api.openai.com/v1",
		Model:   "whisper-1",
		Timeout: 5 * time.Minute,
	}

	if cfg.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test")
	}
	if cfg.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", cfg.Timeout)
	}
}

func TestHTTPClient_Transcribe_WithAllOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify multipart form contains all fields
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm failed: %v", err)
		}

		if r.FormValue("model") != "gpt-4o-transcribe" {
			t.Errorf("model = %q, want gpt-4o-transcribe", r.FormValue("model"))
		}
		if r.FormValue("language") != "es" {
			t.Errorf("language = %q, want es", r.FormValue("language"))
		}
		if r.FormValue("prompt") != "Spanish audio" {
			t.Errorf("prompt = %q, want Spanish audio", r.FormValue("prompt"))
		}
		// Temperature is formatted with 2 decimal places
		temp := r.FormValue("temperature")
		if temp != "0.30" && temp != "0.3" {
			t.Errorf("temperature = %q, want 0.30 or 0.3", temp)
		}

		resp := TranscriptionResponse{
			Text:     "Transcripción en español",
			Language: "es",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData:   strings.NewReader("fake audio"),
		Filename:    "test.mp3",
		Model:       "gpt-4o-transcribe",
		Language:    "es",
		Prompt:      "Spanish audio",
		Temperature: 0.3,
	}

	resp, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if resp.Language != "es" {
		t.Errorf("Language = %q, want es", resp.Language)
	}
}

func TestHTTPClient_Transcribe_NetworkError(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://invalid-domain-that-does-not-exist-12345.com",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio"),
		Filename:  "test.mp3",
	}

	_, err := client.Transcribe(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestHTTPClient_Transcribe_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio"),
		Filename:  "test.mp3",
	}

	_, err := client.Transcribe(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHTTPClient_Transcribe_WithSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := TranscriptionResponse{
			Text:     "Full transcription",
			Language: "en",
			Duration: 10.0,
			Segments: []TranscriptionSegment{
				{ID: 0, Start: 0.0, End: 5.0, Text: "First part"},
				{ID: 1, Start: 5.0, End: 10.0, Text: "Second part"},
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

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio"),
		Filename:  "test.mp3",
	}

	resp, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if len(resp.Segments) != 2 {
		t.Errorf("Segments = %d, want 2", len(resp.Segments))
	}
}

func TestHTTPClient_TranscribeFile_NetworkError(t *testing.T) {
	tmpDir := t.TempDir()
	audioPath := filepath.Join(tmpDir, "test.mp3")
	os.WriteFile(audioPath, []byte("fake audio"), 0644)

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "http://invalid-domain-that-does-not-exist-12345.com",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	_, err := client.TranscribeFile(context.Background(), audioPath, TranscriptionOptions{})
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestHTTPClient_Transcribe_EmptyModelUsesDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			model := r.FormValue("model")
			if model != "whisper-1" {
				t.Errorf("default model = %q, want whisper-1", model)
			}
		}
		resp := TranscriptionResponse{Text: "Test"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		model:      "whisper-1",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio"),
		Filename:  "test.mp3",
		// Model not set - should use default
	}

	_, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
}

func TestHTTPClient_Transcribe_ZeroTemperature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			// Zero temperature should not be sent
			if r.FormValue("temperature") != "" {
				t.Error("zero temperature should not be sent")
			}
		}
		resp := TranscriptionResponse{Text: "Test"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData:   strings.NewReader("fake audio"),
		Filename:    "test.mp3",
		Temperature: 0.0, // Zero should not be sent
	}

	_, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
}

func TestHTTPClient_TranscribeChunks_SingleChunk(t *testing.T) {
	tmpDir := t.TempDir()
	chunkPath := filepath.Join(tmpDir, "chunk1.mp3")
	os.WriteFile(chunkPath, []byte("fake audio"), 0644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := TranscriptionResponse{Text: "Single chunk transcription"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	result, err := client.TranscribeChunks(context.Background(), []string{chunkPath}, TranscriptionOptions{})
	if err != nil {
		t.Fatalf("TranscribeChunks failed: %v", err)
	}
	if result.Text != "Single chunk transcription" {
		t.Errorf("Text = %q, want Single chunk transcription", result.Text)
	}
}

func TestHTTPClient_TranscribeChunks_MultipleChunks(t *testing.T) {
	tmpDir := t.TempDir()
	chunk1 := filepath.Join(tmpDir, "chunk1.mp3")
	chunk2 := filepath.Join(tmpDir, "chunk2.mp3")
	os.WriteFile(chunk1, []byte("fake audio 1"), 0644)
	os.WriteFile(chunk2, []byte("fake audio 2"), 0644)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := TranscriptionResponse{
			Text:     fmt.Sprintf("Chunk %d transcription", callCount),
			Duration: 10.0,
			Segments: []TranscriptionSegment{
				{ID: 0, Start: 0.0, End: 10.0, Text: fmt.Sprintf("Chunk %d", callCount)},
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

	result, err := client.TranscribeChunks(context.Background(), []string{chunk1, chunk2}, TranscriptionOptions{})
	if err != nil {
		t.Fatalf("TranscribeChunks failed: %v", err)
	}
	if !strings.Contains(result.Text, "Chunk 1") || !strings.Contains(result.Text, "Chunk 2") {
		t.Errorf("Text should contain both chunks, got: %q", result.Text)
	}
	if len(result.Segments) != 2 {
		t.Errorf("Segments = %d, want 2", len(result.Segments))
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

func TestHTTPClient_TranscribeChunks_EmptyChunks(t *testing.T) {
	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    "https://example.com",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	_, err := client.TranscribeChunks(context.Background(), []string{}, TranscriptionOptions{})
	if err == nil {
		t.Fatal("expected error for empty chunks")
	}
}

func TestHTTPClient_TranscribeChunks_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	chunk1 := filepath.Join(tmpDir, "chunk1.mp3")
	chunk2 := filepath.Join(tmpDir, "chunk2.mp3")
	os.WriteFile(chunk1, []byte("fake"), 0644)
	os.WriteFile(chunk2, []byte("fake"), 0644)

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

	_, err := client.TranscribeChunks(ctx, []string{chunk1, chunk2}, TranscriptionOptions{})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		model    string
		want     float64
	}{
		{"whisper-1 60 seconds", 60, "whisper-1", 0.006},
		{"gpt-4o-transcribe 120 seconds", 120, "gpt-4o-transcribe", 0.012},
		{"gpt-4o-mini-transcribe 60 seconds", 60, "gpt-4o-mini-transcribe", 0.003},
		{"unknown model defaults", 60, "unknown", 0.006},
		{"zero duration", 0, "whisper-1", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateCost(tt.duration, tt.model)
			if got != tt.want {
				t.Errorf("EstimateCost(%f, %q) = %f, want %f", tt.duration, tt.model, got, tt.want)
			}
		})
	}
}

func TestSupportedFormats(t *testing.T) {
	formats := SupportedFormats()
	if len(formats) == 0 {
		t.Error("SupportedFormats should return formats")
	}

	expected := []string{"flac", "m4a", "mp3", "mp4", "mpeg", "mpga", "oga", "ogg", "wav", "webm"}
	for _, exp := range expected {
		found := false
		for _, f := range formats {
			if f == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("format %q not found in supported formats", exp)
		}
	}
}

func TestIsSupportedFormat(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"test.mp3", true},
		{"test.MP3", true}, // Case insensitive
		{"test.wav", true},
		{"test.flac", true},
		{"test.m4a", true},
		{"test.mp4", true},
		{"test.mpeg", true},
		{"test.mpga", true},
		{"test.oga", true},
		{"test.ogg", true},
		{"test.webm", true},
		{"test.txt", false},
		{"test", false},
		{"test.unknown", false},
		{".mp3", true}, // Extension-only filename is valid
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := IsSupportedFormat(tt.filename)
			if got != tt.want {
				t.Errorf("IsSupportedFormat(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestHTTPClient_Transcribe_SimpleTextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return simple text response (not verbose JSON)
		w.Write([]byte(`{"text":"Simple transcription"}`))
	}))
	defer server.Close()

	client := &HTTPClient{
		apiKey:     "test-key",
		baseURL:    server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	req := TranscriptionRequest{
		AudioData: strings.NewReader("fake audio"),
		Filename:  "test.mp3",
	}

	resp, err := client.Transcribe(context.Background(), req)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if resp.Text != "Simple transcription" {
		t.Errorf("Text = %q, want Simple transcription", resp.Text)
	}
}
