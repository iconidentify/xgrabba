package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGenerateOfflineUIInjectsData verifies that generateOfflineUI properly injects tweet data.
func TestGenerateOfflineUIInjectsData(t *testing.T) {
	// Create test tweets data
	testData := map[string]interface{}{
		"tweets": []map[string]interface{}{
			{
				"tweet_id": "123456789",
				"text":     "This is a test tweet",
				"author": map[string]interface{}{
					"username":     "testuser",
					"display_name": "Test User",
				},
			},
		},
		"total": 1,
	}

	tweetsJSON, err := json.Marshal(testData)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	result := generateOfflineUI(tweetsJSON)

	// Verify result is not empty
	if len(result) == 0 {
		t.Fatal("generateOfflineUI should return non-empty content")
	}

	html := string(result)

	// Verify it contains the injected data
	if !strings.Contains(html, "window.OFFLINE_DATA") {
		t.Error("Result should contain window.OFFLINE_DATA")
	}

	if !strings.Contains(html, "123456789") {
		t.Error("Result should contain the injected tweet ID")
	}

	if !strings.Contains(html, "This is a test tweet") {
		t.Error("Result should contain the injected tweet text")
	}

	// Verify it's valid HTML structure
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("Result should start with DOCTYPE")
	}

	// Verify the script is properly positioned (before </head>)
	headIndex := strings.Index(html, "</head>")
	offlineDataIndex := strings.Index(html, "window.OFFLINE_DATA")
	if offlineDataIndex > headIndex {
		t.Error("OFFLINE_DATA script should appear before </head>")
	}
}

// TestGenerateOfflineUIWithEmptyData verifies handling of empty tweets data.
func TestGenerateOfflineUIWithEmptyData(t *testing.T) {
	testData := map[string]interface{}{
		"tweets": []interface{}{},
		"total":  0,
	}

	tweetsJSON, err := json.Marshal(testData)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	result := generateOfflineUI(tweetsJSON)

	if len(result) == 0 {
		t.Fatal("generateOfflineUI should return content even with empty data")
	}

	html := string(result)

	// Should still have the OFFLINE_DATA injection
	if !strings.Contains(html, "window.OFFLINE_DATA") {
		t.Error("Result should contain window.OFFLINE_DATA even with empty data")
	}
}

// TestGenerateOfflineUIWithMalformedJSON verifies graceful handling of bad JSON.
func TestGenerateOfflineUIWithMalformedJSON(t *testing.T) {
	// Pass malformed JSON
	malformedJSON := []byte(`{"tweets": not valid json}`)

	result := generateOfflineUI(malformedJSON)

	// Should not panic and should return something
	if len(result) == 0 {
		t.Fatal("generateOfflineUI should return content even with malformed JSON")
	}

	html := string(result)

	// Should still be valid HTML
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("Result should still be valid HTML")
	}
}

// TestGenerateOfflineUIPreservesHTMLStructure verifies the HTML structure is maintained.
func TestGenerateOfflineUIPreservesHTMLStructure(t *testing.T) {
	testData := map[string]interface{}{
		"tweets": []interface{}{},
	}
	tweetsJSON, _ := json.Marshal(testData)

	result := generateOfflineUI(tweetsJSON)
	html := string(result)

	// Should have exactly one of each major structural element
	if strings.Count(html, "<html") != 1 {
		t.Error("Should have exactly one <html> tag")
	}

	if strings.Count(html, "</html>") != 1 {
		t.Error("Should have exactly one </html> tag")
	}

	if strings.Count(html, "<head>") != 1 {
		t.Error("Should have exactly one <head> tag")
	}

	if strings.Count(html, "</head>") != 1 {
		t.Error("Should have exactly one </head> tag")
	}

	if strings.Count(html, "<body>") < 1 {
		t.Error("Should have at least one <body> tag")
	}
}

// TestGenerateOfflineUIUsesSharedUI verifies the shared UI package is used.
func TestGenerateOfflineUIUsesSharedUI(t *testing.T) {
	testData := map[string]interface{}{
		"tweets": []interface{}{},
	}
	tweetsJSON, _ := json.Marshal(testData)

	result := generateOfflineUI(tweetsJSON)
	html := string(result)

	// The shared UI should contain the xgrabba branding and key features
	// These are from the main app's index.html that is now embedded in pkg/ui

	// Should have offline mode detection logic
	if !strings.Contains(html, "OFFLINE_MODE") {
		t.Error("Result should use shared UI which contains OFFLINE_MODE detection")
	}
}
