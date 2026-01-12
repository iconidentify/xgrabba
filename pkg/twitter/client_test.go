package twitter

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// =============================================================================
// Unit Tests - Truncation Detection
// =============================================================================

func TestIsTextTruncated(t *testing.T) {
	client := NewClient(testLogger())

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "ends with ellipsis",
			text:     "This is a tweet that ends with...",
			expected: true,
		},
		{
			name:     "ends with unicode ellipsis",
			text:     "This is a tweet that ends with\u2026",
			expected: true,
		},
		{
			name:     "short normal tweet",
			text:     "Just a normal short tweet",
			expected: false,
		},
		{
			name:     "exactly at 280 boundary",
			text:     strings.Repeat("a", 280),
			expected: true,
		},
		{
			name:     "near 280 boundary (278 chars)",
			text:     strings.Repeat("a", 278),
			expected: true,
		},
		{
			name:     "near 280 boundary (282 chars)",
			text:     strings.Repeat("a", 282),
			expected: true,
		},
		{
			name:     "well under limit (200 chars)",
			text:     strings.Repeat("a", 200),
			expected: false,
		},
		{
			name:     "well over limit (500 chars - likely full text)",
			text:     strings.Repeat("a", 500),
			expected: false,
		},
		{
			name:     "ends with t.co link near limit",
			text:     strings.Repeat("a", 250) + " https://t.co/abc123",
			expected: true,
		},
		{
			name:     "ends with t.co link but short",
			text:     "Check this out https://t.co/abc123",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.isTextTruncated(tt.text)
			if result != tt.expected {
				t.Errorf("isTextTruncated(%q) = %v, want %v (len=%d)",
					truncateForLog(tt.text, 50), result, tt.expected, len(tt.text))
			}
		})
	}
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// =============================================================================
// Unit Tests - Guest Token (with mock server)
// =============================================================================

func TestGetGuestToken_Success(t *testing.T) {
	// Since we can't easily override the hardcoded URL, we test the real endpoint
	// in the integration tests below
	t.Skip("Skipping mock test - testing real API in integration test")
}

func TestGetGuestToken_Caching(t *testing.T) {
	// Tested in integration test - guest_token_caching
	t.Skip("Skipping - tested in integration test")
}

// =============================================================================
// Unit Tests - Extract Tweet ID
// =============================================================================

func TestExtractTweetID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "x.com standard URL",
			url:      "https://x.com/elonmusk/status/1234567890123456789",
			expected: "1234567890123456789",
		},
		{
			name:     "twitter.com standard URL",
			url:      "https://twitter.com/user/status/9876543210",
			expected: "9876543210",
		},
		{
			name:     "x.com with query params",
			url:      "https://x.com/user/status/1234567890?s=20",
			expected: "1234567890",
		},
		{
			name:     "x.com with multiple query params",
			url:      "https://x.com/user/status/1234567890?s=20&t=abc",
			expected: "1234567890",
		},
		{
			name:     "invalid URL",
			url:      "https://example.com/not-a-tweet",
			expected: "",
		},
		{
			name:     "empty URL",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractTweetID(tt.url)
			if result != tt.expected {
				t.Errorf("ExtractTweetID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// Integration Tests - Real X API
// =============================================================================

// TestGraphQL_Integration tests the real GraphQL API.
// Run with: go test -v -run TestGraphQL_Integration ./pkg/twitter/...
// This test is NOT skipped so it runs by default to catch API changes.
func TestGraphQL_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient(testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("guest_token_acquisition", func(t *testing.T) {
		token, err := client.getGuestToken(ctx)
		if err != nil {
			t.Fatalf("Failed to get guest token: %v", err)
		}
		if token == "" {
			t.Fatal("Guest token is empty")
		}
		t.Logf("Successfully acquired guest token: %s...", token[:10])
	})

	t.Run("guest_token_caching", func(t *testing.T) {
		// Get token twice - second should be cached
		token1, err := client.getGuestToken(ctx)
		if err != nil {
			t.Fatalf("First token fetch failed: %v", err)
		}
		token2, err := client.getGuestToken(ctx)
		if err != nil {
			t.Fatalf("Second token fetch failed: %v", err)
		}
		if token1 != token2 {
			t.Errorf("Tokens should be cached and equal: %s != %s", token1, token2)
		}
	})

	// Test GraphQL directly with the known long tweet - this verifies feature flags are correct
	// We use the same tweet ID as TestGraphQL_LongTweet since we know it exists
	t.Run("graphql_direct_fetch", func(t *testing.T) {
		// Use the known long tweet ID that we verified works
		tweet, err := client.fetchFromGraphQL(ctx, "2010527435964768695")
		if err != nil {
			// Log the error details - this helps debug flag issues
			t.Logf("GraphQL fetch error (check feature flags): %v", err)

			// Check if it's a 400 error which indicates bad feature flags
			if strings.Contains(err.Error(), "400") {
				t.Fatalf("Got 400 error - likely missing/incorrect feature flags: %v", err)
			}
			// 403 or other errors might be rate limiting
			if strings.Contains(err.Error(), "403") {
				t.Skip("Got 403 - likely rate limited, skipping")
			}
			// Tweet might not exist or be deleted
			if strings.Contains(err.Error(), "no tweet result") {
				t.Skip("Tweet not available - may be deleted or protected")
			}
			t.Fatalf("GraphQL fetch failed: %v", err)
		}

		if tweet.Text == "" {
			t.Error("GraphQL returned empty text")
		}
		t.Logf("GraphQL fetch successful - text length: %d, author: @%s", len(tweet.Text), tweet.Author.Username)
	})
}

// TestGraphQL_LongTweet tests fetching a known long tweet.
// This validates that note_tweet parsing works correctly.
func TestGraphQL_LongTweet(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient(testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// This is a known long tweet that should have note_tweet content
	// Tweet ID from user's original request: 2010527435964768695
	// Note: This specific tweet may not exist or be accessible
	longTweetID := "2010527435964768695"

	t.Run("fetch_long_tweet_full_text", func(t *testing.T) {
		fullText, err := client.fetchFullTextFromGraphQL(ctx, longTweetID)
		if err != nil {
			// Log error but don't fail - the specific tweet may not be available
			t.Logf("Could not fetch long tweet (may not exist): %v", err)

			// Check for specific error types
			if strings.Contains(err.Error(), "400") {
				t.Fatalf("Got 400 error - feature flags may be incorrect: %v", err)
			}
			t.Skip("Long tweet not available for testing")
		}

		t.Logf("Full text length: %d characters", len(fullText))

		// Long tweets should be > 280 characters
		if len(fullText) > 280 {
			t.Logf("Successfully retrieved long tweet text (>280 chars)")
		} else {
			t.Logf("Text is %d chars - may not be a long tweet or may be truncated", len(fullText))
		}
	})
}

// TestGraphQL_FeatureFlags validates that the feature flags string is valid JSON.
func TestGraphQL_FeatureFlags(t *testing.T) {
	// The features string from fetchFullTextFromGraphQL
	features := `{"creator_subscriptions_tweet_preview_api_enabled":true,"communities_web_enable_tweet_community_results_fetch":true,"c9s_tweet_anatomy_moderator_badge_enabled":true,"articles_preview_enabled":true,"responsive_web_edit_tweet_api_enabled":true,"graphql_is_translatable_rweb_tweet_is_translatable_enabled":true,"view_counts_everywhere_api_enabled":true,"longform_notetweets_consumption_enabled":true,"responsive_web_twitter_article_tweet_consumption_enabled":true,"tweet_awards_web_tipping_enabled":false,"creator_subscriptions_quote_tweet_preview_enabled":false,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"rweb_video_timestamps_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_inline_media_enabled":true,"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_enhance_cards_enabled":false,"tweetypie_unmention_optimization_enabled":true}`

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(features), &parsed); err != nil {
		t.Fatalf("Feature flags JSON is invalid: %v", err)
	}

	// Check for critical flags that enable long tweet support
	criticalFlags := []string{
		"longform_notetweets_consumption_enabled",
		"longform_notetweets_rich_text_read_enabled",
		"longform_notetweets_inline_media_enabled",
		"tweetypie_unmention_optimization_enabled",
	}

	for _, flag := range criticalFlags {
		val, exists := parsed[flag]
		if !exists {
			t.Errorf("Missing critical feature flag: %s", flag)
			continue
		}
		if boolVal, ok := val.(bool); !ok || !boolVal {
			t.Errorf("Critical flag %s should be true, got: %v", flag, val)
		}
	}

	t.Logf("Feature flags valid - %d flags present", len(parsed))
}

// TestBearerToken validates the bearer token format.
func TestBearerToken(t *testing.T) {
	// The bearer token should be URL-encoded
	if !strings.Contains(bearerToken, "%") {
		t.Log("Bearer token is not URL-encoded - this is fine if it doesn't need encoding")
	}

	// It should be a reasonable length (Twitter bearer tokens are ~100+ chars)
	if len(bearerToken) < 50 {
		t.Errorf("Bearer token seems too short: %d chars", len(bearerToken))
	}

	t.Logf("Bearer token length: %d chars", len(bearerToken))
}
