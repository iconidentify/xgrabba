package ui

import (
	"strings"
	"testing"
)

// TestIndexHTMLEmbedded verifies that the index.html is embedded and contains expected content.
func TestIndexHTMLEmbedded(t *testing.T) {
	if len(IndexHTML) == 0 {
		t.Fatal("IndexHTML should not be empty")
	}

	html := string(IndexHTML)

	// Verify it's valid HTML
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("IndexHTML should start with DOCTYPE declaration")
	}

	// Verify it contains offline mode detection
	if !strings.Contains(html, "OFFLINE_MODE") {
		t.Error("IndexHTML should contain OFFLINE_MODE detection")
	}

	// Verify it contains window.OFFLINE_DATA reference for offline mode
	if !strings.Contains(html, "OFFLINE_DATA") {
		t.Error("IndexHTML should reference window.OFFLINE_DATA for offline mode")
	}

	// Verify it has the closing head tag (needed for data injection)
	if !strings.Contains(html, "</head>") {
		t.Error("IndexHTML should have closing </head> tag")
	}
}

// TestQuickHTMLEmbedded verifies that the quick.html is embedded and contains expected content.
func TestQuickHTMLEmbedded(t *testing.T) {
	if len(QuickHTML) == 0 {
		t.Fatal("QuickHTML should not be empty")
	}

	html := string(QuickHTML)

	// Verify it's valid HTML
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("QuickHTML should start with DOCTYPE declaration")
	}

	// Verify it's a functional page
	if !strings.Contains(html, "<title>") {
		t.Error("QuickHTML should have a title tag")
	}
}

// TestAdminEventsHTMLEmbedded verifies that the admin_events.html is embedded.
func TestAdminEventsHTMLEmbedded(t *testing.T) {
	if len(AdminEventsHTML) == 0 {
		t.Fatal("AdminEventsHTML should not be empty")
	}

	html := string(AdminEventsHTML)

	// Verify it's valid HTML
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("AdminEventsHTML should start with DOCTYPE declaration")
	}
}

// TestOfflineModeScriptInjection verifies that offline data can be injected into the UI.
func TestOfflineModeScriptInjection(t *testing.T) {
	html := string(IndexHTML)

	// Simulate the injection pattern used in the viewer
	testData := `{"tweets":[{"tweet_id":"123","text":"Test tweet"}]}`
	dataScript := `<script>
window.OFFLINE_DATA = ` + testData + `;
</script>
</head>`

	result := strings.Replace(html, "</head>", dataScript, 1)

	// Verify injection worked
	if !strings.Contains(result, "window.OFFLINE_DATA") {
		t.Error("Failed to inject OFFLINE_DATA into HTML")
	}

	if !strings.Contains(result, "Test tweet") {
		t.Error("Failed to inject test data into HTML")
	}

	// Verify we still have exactly one </head> tag
	if strings.Count(result, "</head>") != 1 {
		t.Error("HTML should have exactly one </head> tag after injection")
	}
}
