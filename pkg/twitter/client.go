package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// bearerToken is the public bearer token used by X.com's web client.
// This is not a secret - it's embedded in X.com's main.js and is the same for all users.
const bearerToken = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

// GraphQL query IDs - these may change periodically as X updates their API
// To find current IDs: curl -s "https://x.com" | extract main.js URL, then search for operationName
const (
	defaultTweetResultByRestIDQueryID = "Xl5pC_lBk_gcO2ItU39DQw"
	defaultTweetDetailQueryID         = "nK2WM0mHJKd2-jb6qhmfWA" // TweetDetail - includes article content_state
)

// defaultGraphQLFeatures is used when we don't have browser-observed feature flags.
// Keep this as a fallback so GraphQL continues to work even without extension forwarding.
const defaultGraphQLFeatures = `{"creator_subscriptions_tweet_preview_api_enabled":true,"communities_web_enable_tweet_community_results_fetch":true,"c9s_tweet_anatomy_moderator_badge_enabled":true,"articles_preview_enabled":true,"responsive_web_edit_tweet_api_enabled":true,"graphql_is_translatable_rweb_tweet_is_translatable_enabled":true,"view_counts_everywhere_api_enabled":true,"longform_notetweets_consumption_enabled":true,"responsive_web_twitter_article_tweet_consumption_enabled":true,"tweet_awards_web_tipping_enabled":false,"creator_subscriptions_quote_tweet_preview_enabled":false,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"rweb_video_timestamps_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_inline_media_enabled":true,"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_enhance_cards_enabled":false,"tweetypie_unmention_optimization_enabled":true,"responsive_web_grok_analysis_button_from_backend":false,"premium_content_api_read_enabled":false,"post_ctas_fetch_enabled":false,"profile_label_improvements_pcf_label_in_post_enabled":false,"responsive_web_grok_image_annotation_enabled":false,"responsive_web_grok_community_note_auto_translation_is_enabled":false,"responsive_web_grok_show_grok_translated_post":false,"responsive_web_profile_redirect_enabled":false,"responsive_web_jetfuel_frame":false,"responsive_web_grok_analyze_button_fetch_trends_enabled":false,"responsive_web_grok_annotations_enabled":false,"responsive_web_grok_imagine_annotation_enabled":false,"responsive_web_grok_analyze_post_followups_enabled":false,"responsive_web_grok_share_attachment_enabled":false}`

// Client fetches tweet data from X.com.
type Client struct {
	httpClient *http.Client
	userAgent  string
	logger     *slog.Logger

	// Guest token caching
	guestToken       string
	guestTokenExpiry time.Time
	guestTokenMu     sync.Mutex

	// Dynamic GraphQL query ID (auto-refreshes when stale)
	graphQLQueryID       string
	graphQLQueryIDExpiry time.Time
	graphQLQueryIDMu     sync.Mutex

	// Bookmarks GraphQL query ID (separate operation; also may change)
	bookmarksQueryID       string
	bookmarksQueryIDExpiry time.Time
	bookmarksQueryIDMu     sync.Mutex
}

func normalizeTweetText(s string) string {
	// X responses can occasionally include HTML entity encoding (e.g. "&amp;").
	// Normalize so stored/searchable text matches what the UI expects.
	if strings.IndexByte(s, '&') == -1 {
		return s
	}
	return html.UnescapeString(s)
}

// NewClient creates a new Twitter client.
func NewClient(logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		logger:    logger,
	}
}

// getGraphQLQueryIDWithSource returns the current TweetResultByRestId query ID,
// plus where that value came from (browser|cached|default). This is for observability.
func (c *Client) getGraphQLQueryIDWithSource() (queryID string, source string) {
	// Prefer browser-observed query IDs when present.
	if qid := c.getBrowserQueryID("TweetResultByRestId"); qid != "" {
		return qid, "browser"
	}

	c.graphQLQueryIDMu.Lock()
	defer c.graphQLQueryIDMu.Unlock()

	if c.graphQLQueryID != "" && time.Now().Before(c.graphQLQueryIDExpiry) {
		return c.graphQLQueryID, "cached"
	}
	return defaultTweetResultByRestIDQueryID, "default"
}

// getBookmarksQueryIDWithSource returns the current Bookmarks query ID,
// plus where that value came from (browser|cached|default).
func (c *Client) getBookmarksQueryIDWithSource() (queryID string, source string) {
	// Prefer browser-observed query IDs when present.
	if qid := c.getBrowserQueryID("Bookmarks"); qid != "" {
		return qid, "browser"
	}

	c.bookmarksQueryIDMu.Lock()
	defer c.bookmarksQueryIDMu.Unlock()

	if c.bookmarksQueryID != "" && time.Now().Before(c.bookmarksQueryIDExpiry) {
		return c.bookmarksQueryID, "cached"
	}
	return defaultBookmarksQueryID, "default"
}

// getGraphQLFeaturesWithSource returns the features JSON blob plus where it came from (browser|default).
func (c *Client) getGraphQLFeaturesWithSource() (features string, source string) {
	// Prefer browser-observed feature flags, but merge them into our known-good defaults.
	//
	// X's API is strict: many feature keys "cannot be null". The browser-captured feature
	// blob can be partial (and sometimes contains nulls), so we:
	// - start with defaultGraphQLFeatures (full set)
	// - overlay browser flags ONLY when the value is a strict boolean
	// - drop null / non-boolean values to avoid validation failures
	if ff := c.getBrowserFeatureFlags(); len(ff) > 0 {
		merged, ok := mergeGraphQLFeatureFlags(defaultGraphQLFeatures, ff)
		if ok {
			return merged, "merged(browser+default)"
		}
		// If merge fails, fall back to defaults (never pass through a potentially-invalid blob).
		c.logger.Warn("failed to merge browser feature flags; falling back to default")
	}
	return defaultGraphQLFeatures, "default"
}

// mergeGraphQLFeatureFlags merges a browser-observed "features" object into a default features object.
// Only boolean overrides are applied; null / non-boolean values are ignored.
func mergeGraphQLFeatureFlags(defaultJSON string, browser json.RawMessage) (string, bool) {
	if len(browser) == 0 {
		return "", false
	}

	var base map[string]interface{}
	if err := json.Unmarshal([]byte(defaultJSON), &base); err != nil {
		return "", false
	}

	var observed map[string]interface{}
	if err := json.Unmarshal(browser, &observed); err != nil {
		return "", false
	}

	// Overlay only strict booleans from observed onto base.
	for k, v := range observed {
		if b, ok := v.(bool); ok {
			base[k] = b
		}
	}

	out, err := json.Marshal(base)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// refreshGraphQLQueryID fetches the current query ID from X's main.js.
// This is called when we detect a stale query ID (e.g., "Query: Unspecified" error).
func (c *Client) refreshGraphQLQueryID(ctx context.Context) (string, error) {
	c.logger.Info("attempting to refresh GraphQL query ID from X.com")

	// First, fetch X.com's homepage to find the main.js URL
	req, err := http.NewRequestWithContext(ctx, "GET", "https://x.com", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch x.com: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Find main.js URL (format: https://abs.twimg.com/responsive-web/client-web/main.HASH.js)
	mainJSRegex := regexp.MustCompile(`https://abs\.twimg\.com/responsive-web/client-web[^"]*main\.[a-zA-Z0-9]+\.js`)
	mainJSMatch := mainJSRegex.FindString(string(body))
	if mainJSMatch == "" {
		return "", fmt.Errorf("could not find main.js URL in X.com response")
	}

	// Fetch main.js
	req, err = http.NewRequestWithContext(ctx, "GET", mainJSMatch, nil)
	if err != nil {
		return "", fmt.Errorf("create main.js request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch main.js: %w", err)
	}
	defer resp.Body.Close()

	jsBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read main.js: %w", err)
	}

	// Find TweetResultByRestId query ID (format: queryId:"XXXXX",operationName:"TweetResultByRestId")
	queryIDRegex := regexp.MustCompile(`queryId:"([a-zA-Z0-9_-]+)",operationName:"TweetResultByRestId"`)
	queryIDMatch := queryIDRegex.FindSubmatch(jsBody)
	if queryIDMatch == nil {
		return "", fmt.Errorf("could not find TweetResultByRestId query ID in main.js")
	}

	newQueryID := string(queryIDMatch[1])
	c.logger.Info("found new GraphQL query ID", "query_id", newQueryID)

	// Cache the new query ID for 24 hours
	c.graphQLQueryIDMu.Lock()
	c.graphQLQueryID = newQueryID
	c.graphQLQueryIDExpiry = time.Now().Add(24 * time.Hour)
	c.graphQLQueryIDMu.Unlock()

	return newQueryID, nil
}

// refreshBookmarksQueryID fetches the current Bookmarks query ID from X's main.js.
func (c *Client) refreshBookmarksQueryID(ctx context.Context) (string, error) {
	c.logger.Info("attempting to refresh Bookmarks GraphQL query ID from X.com")

	// Fetch X.com's homepage to find the main.js URL
	req, err := http.NewRequestWithContext(ctx, "GET", "https://x.com", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch x.com: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	mainJSRegex := regexp.MustCompile(`https://abs\.twimg\.com/responsive-web/client-web[^"]*main\.[a-zA-Z0-9]+\.js`)
	mainJSMatch := mainJSRegex.FindString(string(body))
	if mainJSMatch == "" {
		return "", fmt.Errorf("could not find main.js URL in X.com response")
	}

	req, err = http.NewRequestWithContext(ctx, "GET", mainJSMatch, nil)
	if err != nil {
		return "", fmt.Errorf("create main.js request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch main.js: %w", err)
	}
	defer resp.Body.Close()

	jsBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read main.js: %w", err)
	}

	// Find Bookmarks query ID (format: queryId:"XXXXX",operationName:"Bookmarks")
	queryIDRegex := regexp.MustCompile(`queryId:"([a-zA-Z0-9_-]+)",operationName:"Bookmarks"`)
	queryIDMatch := queryIDRegex.FindSubmatch(jsBody)
	if queryIDMatch == nil {
		return "", fmt.Errorf("could not find Bookmarks query ID in main.js")
	}

	newQueryID := string(queryIDMatch[1])
	c.logger.Info("found new Bookmarks GraphQL query ID", "query_id", newQueryID)

	// Cache for 24 hours
	c.bookmarksQueryIDMu.Lock()
	c.bookmarksQueryID = newQueryID
	c.bookmarksQueryIDExpiry = time.Now().Add(24 * time.Hour)
	c.bookmarksQueryIDMu.Unlock()

	return newQueryID, nil
}

// clearGraphQLQueryID invalidates the cached query ID so next call uses default or refreshes.
func (c *Client) clearGraphQLQueryID() {
	c.graphQLQueryIDMu.Lock()
	c.graphQLQueryID = ""
	c.graphQLQueryIDExpiry = time.Time{}
	c.graphQLQueryIDMu.Unlock()
}

// FetchTweet retrieves tweet data from X.com.
func (c *Client) FetchTweet(ctx context.Context, tweetURL string) (*domain.Tweet, error) {
	tweetID := ExtractTweetID(tweetURL)
	if tweetID == "" {
		return nil, fmt.Errorf("could not extract tweet ID from URL: %s", tweetURL)
	}

	// Try syndication API first (works for public tweets, fast, no auth)
	result, err := c.fetchFromSyndication(ctx, tweetID)
	if err == nil {
		tweet := result.Tweet
		tweet.URL = tweetURL
		tweet.Text = normalizeTweetText(tweet.Text)

		// For note tweets (long-form), syndication text is definitely truncated.
		// We MUST fetch full text via GraphQL. For regular tweets, try GraphQL but don't require it.
		fullText, gqlErr := c.fetchFullTextFromGraphQL(ctx, tweetID)

		if gqlErr == nil && fullText != "" {
			fullText = normalizeTweetText(fullText)
			if len(fullText) > len(tweet.Text) {
				c.logger.Info("GraphQL returned longer text, using it", "tweet_id", tweetID, "syndication_len", len(tweet.Text), "graphql_len", len(fullText), "is_note_tweet", result.IsNoteTweet)
				tweet.Text = fullText
			} else {
				c.logger.Debug("GraphQL text not longer", "tweet_id", tweetID, "syndication_len", len(tweet.Text), "graphql_len", len(fullText))
			}
		} else if gqlErr != nil {
			if result.IsNoteTweet {
				// This is a note tweet but GraphQL failed - this is a serious issue.
				// The stored text will be truncated. Log loudly.
				c.logger.Error("GraphQL failed for note tweet - text will be truncated!",
					"tweet_id", tweetID,
					"error", gqlErr,
					"truncated_len", len(tweet.Text),
					"hint", "The GraphQL query ID may be outdated - check X's main.js for TweetResultByRestId")
			} else {
				c.logger.Warn("GraphQL fetch failed", "tweet_id", tweetID, "error", gqlErr)
			}
		}

		// Optional enrichment pass: keep the normal syndication flow as the primary source,
		// but attempt to fill in missing fields from GraphQL when available.
		//
		// This is intentionally best-effort and should never cause the tweet to fail if
		// GraphQL is blocked/rate-limited.
		c.enrichFromGraphQL(ctx, tweetID, tweet)

		// Best-effort: if author handle is missing (NSFW/visibility edge cases),
		// derive it from the URL so the archive remains human-friendly.
		applyAuthorFromURL(tweet, tweetURL)
		tweet.Text = normalizeTweetText(tweet.Text)

		return tweet, nil
	}

	// If syndication fails, try GraphQL directly
	tweet, graphqlErr := c.fetchFromGraphQL(ctx, tweetID)
	if graphqlErr == nil {
		tweet.URL = tweetURL
		applyAuthorFromURL(tweet, tweetURL)
		// If we still don't have an avatar URL, try a profile-page fallback.
		// This is useful when API endpoints are blocked but the public profile HTML loads.
		c.enrichAvatarFromProfilePage(ctx, tweet)
		tweet.Text = normalizeTweetText(tweet.Text)
		return tweet, nil
	}

	return nil, fmt.Errorf("failed to fetch tweet (syndication: %v, graphql: %v)", err, graphqlErr)
}

// enrichAvatarFromProfilePage attempts to populate the author's avatar URL by fetching the public profile HTML.
// This is best-effort and should never fail the tweet fetch.
func (c *Client) enrichAvatarFromProfilePage(ctx context.Context, tweet *domain.Tweet) {
	if tweet == nil {
		return
	}
	if tweet.Author.AvatarURL != "" {
		return
	}
	username := strings.TrimSpace(tweet.Author.Username)
	if username == "" {
		return
	}
	// Keep this bounded; it's a fallback only.
	pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	avatar, err := c.fetchProfileAvatarFromHTML(pctx, username)
	if err != nil || avatar == "" {
		return
	}
	tweet.Author.AvatarURL = avatar
}

func (c *Client) fetchProfileAvatarFromHTML(ctx context.Context, username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf("missing username")
	}
	profileURL := "https://x.com/" + url.PathEscape(username)

	req, err := http.NewRequestWithContext(ctx, "GET", profileURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// If we have browser creds, include them (can help with bot checks), but still target HTML.
	if h := c.getBrowserHeaders(); h != nil {
		for k, v := range h {
			req.Header[k] = v
		}
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Del("Content-Type")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("profile fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	htmlBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB cap
	if err != nil {
		return "", err
	}
	html := string(htmlBytes)

	// Prefer og:image / twitter:image. These frequently point to pbs.twimg.com/profile_images/*.
	reMeta := regexp.MustCompile(`(?i)<meta[^>]+(?:property|name)=["'](?:og:image|twitter:image)["'][^>]+content=["']([^"']+)["']`)
	if m := reMeta.FindStringSubmatch(html); len(m) > 1 {
		return normalizeProfileImageURL(m[1]), nil
	}

	// Fallback: any pbs.twimg.com/profile_images URL in the HTML.
	reImg := regexp.MustCompile(`(?i)https://pbs\.twimg\.com/profile_images/[^"'\\s]+`)
	if m := reImg.FindString(html); m != "" {
		return normalizeProfileImageURL(m), nil
	}

	return "", fmt.Errorf("no avatar found in profile html")
}

func normalizeProfileImageURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	// Upgrade common low-res suffixes when present.
	u = strings.ReplaceAll(u, "_normal.", "_400x400.")
	return u
}

// enrichFromGraphQL fills in missing tweet fields from GraphQL without changing the primary
// syndication-based flow. It only runs when syndication succeeded.
func (c *Client) enrichFromGraphQL(ctx context.Context, tweetID string, base *domain.Tweet) {
	if base == nil || tweetID == "" {
		return
	}
	// Only attempt if browser credentials are available (guest token is often insufficient for restricted tweets).
	if !c.HasBrowserCredentials() {
		return
	}

	// Only attempt if we have obvious gaps to fill.
	needs := base.Metrics.Views == 0 || base.Author.AvatarURL == "" || base.Author.DisplayName == "" || len(base.Media) == 0
	if !needs {
		return
	}

	// Keep this bounded so we don't regress the fast path.
	enrichCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	gqlTweet, err := c.fetchFromGraphQLWithRetry(enrichCtx, tweetID, false)
	if err != nil || gqlTweet == nil {
		return
	}

	// Merge fields conservatively.
	if base.Metrics.Views == 0 && gqlTweet.Metrics.Views != 0 {
		base.Metrics.Views = gqlTweet.Metrics.Views
	}
	if base.Author.AvatarURL == "" && gqlTweet.Author.AvatarURL != "" {
		base.Author.AvatarURL = gqlTweet.Author.AvatarURL
	}
	if (base.Author.DisplayName == "" || base.Author.DisplayName == "unknown" || strings.HasPrefix(base.Author.DisplayName, "user_")) && gqlTweet.Author.DisplayName != "" {
		base.Author.DisplayName = gqlTweet.Author.DisplayName
	}
	if (base.Author.Username == "" || base.Author.Username == "unknown" || strings.HasPrefix(base.Author.Username, "user_")) && gqlTweet.Author.Username != "" {
		base.Author.Username = gqlTweet.Author.Username
	}
	if base.Author.ID == "" && gqlTweet.Author.ID != "" {
		base.Author.ID = gqlTweet.Author.ID
	}
	if len(base.Media) == 0 && len(gqlTweet.Media) > 0 {
		base.Media = gqlTweet.Media
		base.MediaTotal = len(gqlTweet.Media)
	}

	// Merge article fields if this is an article
	if gqlTweet.ContentType == domain.ContentTypeArticle {
		base.ContentType = gqlTweet.ContentType
		if gqlTweet.ArticleTitle != "" {
			base.ArticleTitle = gqlTweet.ArticleTitle
		}
		if gqlTweet.ArticleBody != "" {
			base.ArticleBody = gqlTweet.ArticleBody
		}
		if gqlTweet.ArticleHTML != "" {
			base.ArticleHTML = gqlTweet.ArticleHTML
		}
		if len(gqlTweet.ArticleImages) > 0 {
			base.ArticleImages = gqlTweet.ArticleImages
		}
		if gqlTweet.WordCount > 0 {
			base.WordCount = gqlTweet.WordCount
		}
		if gqlTweet.ReadingMinutes > 0 {
			base.ReadingMinutes = gqlTweet.ReadingMinutes
		}
	}
}

// applyAuthorFromURL fills in missing author fields using the tweet URL path.
// This is a pragmatic fallback when GraphQL user lookups are blocked (e.g., Cloudflare)
// and the tweet result omits screen_name/profile image fields.
func applyAuthorFromURL(tweet *domain.Tweet, tweetURL string) {
	if tweet == nil || tweetURL == "" {
		return
	}
	u := ExtractUsernameFromTweetURL(tweetURL)
	if u == "" {
		return
	}

	// If username is missing or a placeholder, prefer the URL-derived handle.
	if tweet.Author.Username == "" || tweet.Author.Username == "unknown" || strings.HasPrefix(tweet.Author.Username, "user_") {
		tweet.Author.Username = u
	}
	// If display name is missing or placeholder-like, use the handle.
	if tweet.Author.DisplayName == "" || tweet.Author.DisplayName == "unknown" || strings.HasPrefix(tweet.Author.DisplayName, "user_") {
		tweet.Author.DisplayName = u
	}
}

// isTextTruncated checks if tweet text appears to be truncated (long tweet/note).
func (c *Client) isTextTruncated(text string) bool {
	// Common truncation indicators
	if strings.HasSuffix(text, "...") || strings.HasSuffix(text, "\u2026") {
		return true
	}

	// X Premium long tweets can be up to 25,000 chars; old limit was 280
	// If text is near the character limit, check for truncation signs
	textLen := len(text)

	// Check if text ends with a t.co link (common truncation pattern)
	// Long tweets often get cut off with the media link at the end
	if textLen >= 250 && textLen <= 320 {
		// Ends with t.co link - likely truncated if near limit
		if strings.Contains(text, "https://t.co/") && strings.HasSuffix(text, strings.TrimSpace(text[strings.LastIndex(text, "https://t.co/"):])) {
			return true
		}
	}

	// If text is exactly at common boundaries, might be truncated
	if textLen >= 275 && textLen <= 285 {
		return true
	}

	return false
}

// syndicationResult wraps the tweet and metadata from syndication API.
type syndicationResult struct {
	Tweet       *domain.Tweet
	IsNoteTweet bool // True if this is a long-form tweet (note_tweet field present)
	IsArticle   bool // True if this appears to be a full article (very long content)
}

// fetchFromSyndication uses Twitter's public syndication API.
func (c *Client) fetchFromSyndication(ctx context.Context, tweetID string) (*syndicationResult, error) {
	url := fmt.Sprintf("https://cdn.syndication.twimg.com/tweet-result?id=%s&token=0", tweetID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var syndicationResp syndicationResponse
	if err := json.NewDecoder(resp.Body).Decode(&syndicationResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	tweet, err := c.parseSyndicationResponse(tweetID, &syndicationResp)
	if err != nil {
		return nil, err
	}

	return &syndicationResult{
		Tweet:       tweet,
		IsNoteTweet: syndicationResp.NoteTweet != nil && syndicationResp.NoteTweet.ID != "",
	}, nil
}

// syndicationResponse is the response from the syndication API.
type syndicationResponse struct {
	ID        string `json:"id_str"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	User      struct {
		ID              string `json:"id_str"`
		Name            string `json:"name"`
		ScreenName      string `json:"screen_name"`
		ProfileImageURL string `json:"profile_image_url_https"`
		Verified        bool   `json:"verified"`
		IsBlueVerified  bool   `json:"is_blue_verified"`
		FollowersCount  int    `json:"followers_count"`
		FriendsCount    int    `json:"friends_count"`
		StatusesCount   int    `json:"statuses_count"`
		Description     string `json:"description"`
	} `json:"user"`
	Entities struct {
		Media []struct {
			ID            string `json:"id_str"`
			Type          string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
			URL           string `json:"url"`
			ExpandedURL   string `json:"expanded_url"`
			Sizes         struct {
				Large struct {
					W int `json:"w"`
					H int `json:"h"`
				} `json:"large"`
			} `json:"sizes"`
		} `json:"media"`
	} `json:"entities"`
	ExtendedEntities struct {
		Media []struct {
			ID            string `json:"id_str"`
			Type          string `json:"type"`
			MediaURLHTTPS string `json:"media_url_https"`
			VideoInfo     struct {
				DurationMillis int `json:"duration_millis"`
				Variants       []struct {
					Bitrate     int    `json:"bitrate"`
					ContentType string `json:"content_type"`
					URL         string `json:"url"`
				} `json:"variants"`
			} `json:"video_info"`
			Sizes struct {
				Large struct {
					W int `json:"w"`
					H int `json:"h"`
				} `json:"large"`
			} `json:"sizes"`
			ExtAltText string `json:"ext_alt_text"`
		} `json:"media"`
	} `json:"extended_entities"`
	FavoriteCount     int    `json:"favorite_count"`
	RetweetCount      int    `json:"retweet_count"`
	ReplyCount        int    `json:"reply_count"`
	QuoteCount        int    `json:"quote_count"`
	InReplyToStatusID string `json:"in_reply_to_status_id_str"`
	QuotedStatusID    string `json:"quoted_status_id_str"`
	Photos            []struct {
		URL                string `json:"url"`
		Width              int    `json:"width"`
		Height             int    `json:"height"`
		AccessibilityLabel string `json:"accessibilityLabel"`
	} `json:"photos"`
	Video struct {
		Variants []struct {
			Type string `json:"type"`
			Src  string `json:"src"`
		} `json:"variants"`
		Poster     string `json:"poster"`
		DurationMs int    `json:"durationMs"`
	} `json:"video"`
	MediaDetails []struct {
		MediaURLHTTPS string `json:"media_url_https"`
		Type          string `json:"type"`
		VideoInfo     struct {
			DurationMillis int `json:"duration_millis"`
			Variants       []struct {
				Bitrate     int    `json:"bitrate"`
				ContentType string `json:"content_type"`
				URL         string `json:"url"`
			} `json:"variants"`
		} `json:"video_info"`
	} `json:"mediaDetails"`
	// NoteTweet is present when this is a long-form tweet (X Premium feature, up to 25k chars).
	// If this field has an ID, the text field is truncated and we MUST fetch via GraphQL.
	NoteTweet *struct {
		ID string `json:"id"`
	} `json:"note_tweet,omitempty"`
}

func (c *Client) parseSyndicationResponse(tweetID string, resp *syndicationResponse) (*domain.Tweet, error) {
	// Validate author data is present - if missing, tweet is likely deleted/suspended
	if resp.User.ScreenName == "" {
		return nil, fmt.Errorf("tweet author data unavailable (account may be suspended or deleted)")
	}

	// Parse created_at time
	postedAt, _ := time.Parse(time.RubyDate, resp.CreatedAt)
	if postedAt.IsZero() {
		postedAt = time.Now()
	}

	tweet := &domain.Tweet{
		ID:       domain.TweetID(tweetID),
		Text:     resp.Text,
		PostedAt: postedAt,
		Author: domain.Author{
			ID:             resp.User.ID,
			Username:       resp.User.ScreenName,
			DisplayName:    resp.User.Name,
			AvatarURL:      resp.User.ProfileImageURL,
			Verified:       resp.User.Verified || resp.User.IsBlueVerified,
			FollowerCount:  resp.User.FollowersCount,
			FollowingCount: resp.User.FriendsCount,
			TweetCount:     resp.User.StatusesCount,
			Description:    resp.User.Description,
		},
		Metrics: domain.TweetMetrics{
			Likes:    resp.FavoriteCount,
			Retweets: resp.RetweetCount,
			Replies:  resp.ReplyCount,
			Quotes:   resp.QuoteCount,
		},
		Status:    domain.ArchiveStatusPending,
		CreatedAt: time.Now(),
	}

	// Set reply/quote references
	if resp.InReplyToStatusID != "" {
		replyTo := domain.TweetID(resp.InReplyToStatusID)
		tweet.ReplyTo = &replyTo
	}
	if resp.QuotedStatusID != "" {
		quoted := domain.TweetID(resp.QuotedStatusID)
		tweet.QuotedTweet = &quoted
	}

	// Parse media - try multiple sources
	tweet.Media = c.parseMedia(resp)

	return tweet, nil
}

func (c *Client) parseMedia(resp *syndicationResponse) []domain.Media {
	var media []domain.Media
	seen := make(map[string]bool)

	// Collect all video variants from all sources to find the absolute best quality
	type videoCandidate struct {
		url        string
		bitrate    int
		previewURL string
		duration   int
		width      int
		height     int
		mediaType  domain.MediaType
	}
	var videoCandidates []videoCandidate

	// Parse from photos array (new format)
	for i, photo := range resp.Photos {
		if seen[photo.URL] {
			continue
		}
		seen[photo.URL] = true

		media = append(media, domain.Media{
			ID:      fmt.Sprintf("photo_%d", i),
			Type:    domain.MediaTypeImage,
			URL:     photo.URL,
			Width:   photo.Width,
			Height:  photo.Height,
			AltText: photo.AccessibilityLabel,
		})
	}

	// Collect from video object (new format)
	if resp.Video.Poster != "" {
		for _, v := range resp.Video.Variants {
			if v.Type == "video/mp4" || strings.Contains(v.Src, ".mp4") {
				bitrate := extractBitrateFromURL(v.Src)
				videoCandidates = append(videoCandidates, videoCandidate{
					url:        v.Src,
					bitrate:    bitrate,
					previewURL: resp.Video.Poster,
					duration:   resp.Video.DurationMs / 1000,
					mediaType:  domain.MediaTypeVideo,
				})
			}
		}
	}

	// Collect from mediaDetails (another format) - has explicit bitrates
	for _, md := range resp.MediaDetails {
		if md.Type == "video" || md.Type == "animated_gif" {
			mediaType := domain.MediaTypeVideo
			if md.Type == "animated_gif" {
				mediaType = domain.MediaTypeGIF
			}
			for _, v := range md.VideoInfo.Variants {
				if v.ContentType == "video/mp4" {
					videoCandidates = append(videoCandidates, videoCandidate{
						url:        v.URL,
						bitrate:    v.Bitrate,
						previewURL: md.MediaURLHTTPS,
						duration:   md.VideoInfo.DurationMillis / 1000,
						mediaType:  mediaType,
					})
				}
			}
		} else if md.Type == "photo" {
			if seen[md.MediaURLHTTPS] {
				continue
			}
			seen[md.MediaURLHTTPS] = true

			media = append(media, domain.Media{
				ID:   fmt.Sprintf("photo_%d", len(media)),
				Type: domain.MediaTypeImage,
				URL:  md.MediaURLHTTPS,
			})
		}
	}

	// Collect from extended_entities (legacy format) - also has explicit bitrates
	for _, em := range resp.ExtendedEntities.Media {
		if em.Type == "video" || em.Type == "animated_gif" {
			mediaType := domain.MediaTypeVideo
			if em.Type == "animated_gif" {
				mediaType = domain.MediaTypeGIF
			}
			for _, v := range em.VideoInfo.Variants {
				if v.ContentType == "video/mp4" {
					videoCandidates = append(videoCandidates, videoCandidate{
						url:        v.URL,
						bitrate:    v.Bitrate,
						previewURL: em.MediaURLHTTPS,
						duration:   em.VideoInfo.DurationMillis / 1000,
						width:      em.Sizes.Large.W,
						height:     em.Sizes.Large.H,
						mediaType:  mediaType,
					})
				}
			}
		} else if em.Type == "photo" {
			if seen[em.MediaURLHTTPS] {
				continue
			}
			seen[em.MediaURLHTTPS] = true

			media = append(media, domain.Media{
				ID:      em.ID,
				Type:    domain.MediaTypeImage,
				URL:     em.MediaURLHTTPS,
				Width:   em.Sizes.Large.W,
				Height:  em.Sizes.Large.H,
				AltText: em.ExtAltText,
			})
		}
	}

	// Now select the BEST quality video from all candidates
	if len(videoCandidates) > 0 {
		// Sort by bitrate descending to get highest quality first
		sort.Slice(videoCandidates, func(i, j int) bool {
			return videoCandidates[i].bitrate > videoCandidates[j].bitrate
		})

		// Take the best one
		best := videoCandidates[0]
		media = append(media, domain.Media{
			ID:         "video_0",
			Type:       best.mediaType,
			URL:        best.url,
			PreviewURL: best.previewURL,
			Duration:   best.duration,
			Width:      best.width,
			Height:     best.height,
			Bitrate:    best.bitrate,
		})
	}

	return media
}

// ExtractTweetID extracts the tweet ID from various URL formats.
func ExtractTweetID(url string) string {
	// Match patterns like:
	// https://x.com/user/status/1234567890
	// https://twitter.com/user/status/1234567890
	// https://x.com/user/status/1234567890?s=20
	re := regexp.MustCompile(`(?:twitter\.com|x\.com)/\w+/status/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// articleContent holds fetched article data
type articleContent struct {
	Title  string
	Body   string
	Images []domain.ArticleImage
}

// enrichArticleContent fetches full article content via TweetDetail and populates the tweet.
// This is called when a tweet is detected as an article to get the full content_state.
func (c *Client) enrichArticleContent(ctx context.Context, tweet *domain.Tweet) {
	if tweet == nil || tweet.ContentType != domain.ContentTypeArticle {
		return
	}

	tweetID := string(tweet.ID)
	content, err := c.fetchArticleViaTweetDetail(ctx, tweetID)
	if err != nil {
		c.logger.Debug("failed to fetch full article content via TweetDetail",
			"tweet_id", tweetID,
			"error", err,
		)
		return
	}

	// Update article fields with full content
	if content.Title != "" {
		tweet.ArticleTitle = content.Title
	}
	if content.Body != "" {
		tweet.ArticleBody = content.Body
		words := strings.Fields(tweet.ArticleBody)
		tweet.WordCount = len(words)
		tweet.ReadingMinutes = tweet.CalculateReadingTime()
	}
	if len(content.Images) > 0 {
		tweet.ArticleImages = content.Images
	}

	c.logger.Info("enriched article with full content from TweetDetail",
		"tweet_id", tweetID,
		"title", tweet.ArticleTitle,
		"body_length", len(tweet.ArticleBody),
		"word_count", tweet.WordCount,
		"image_count", len(tweet.ArticleImages),
	)
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// fetchArticleViaTweetDetail fetches full article content using the TweetDetail GraphQL endpoint.
// This endpoint returns article content_state with full text blocks when withArticleRichContentState=true.
func (c *Client) fetchArticleViaTweetDetail(ctx context.Context, tweetID string) (*articleContent, error) {
	headers, source, err := c.getGraphQLAuthHeaders(ctx)
	if err != nil {
		return nil, err
	}

	// Use TweetDetail query ID - this endpoint supports withArticleRichContentState
	queryID := defaultTweetDetailQueryID
	if qid := c.getBrowserQueryID("TweetDetail"); qid != "" {
		queryID = qid
	}

	// Build variables with focalTweetId (TweetDetail uses this instead of tweetId)
	variables := fmt.Sprintf(`{"focalTweetId":"%s","with_rux_injections":false,"rankingMode":"Relevance","includePromotedContent":true,"withCommunity":true,"withQuickPromoteEligibilityTweetFields":true,"withBirdwatchNotes":true,"withVoice":true}`, tweetID)

	features, _ := c.getGraphQLFeaturesWithSource()

	// Field toggles - withArticleRichContentState is the key for getting full article content
	fieldToggles := `{"withArticleRichContentState":true,"withArticlePlainText":false,"withGrokAnalyze":false,"withDisallowedReplyControls":false}`

	reqURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/TweetDetail?variables=%s&features=%s&fieldToggles=%s",
		queryID,
		url.QueryEscape(variables),
		url.QueryEscape(features),
		url.QueryEscape(fieldToggles))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create TweetDetail request: %w", err)
	}

	// Copy headers to request
	for k, v := range headers {
		req.Header[k] = v
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TweetDetail request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TweetDetail error (status %d, auth: %s): %s", resp.StatusCode, source, truncateText(string(body), 200))
	}

	// TweetDetail returns a timeline structure, not a simple tweetResult
	// We need to parse the timeline and find the focal tweet
	var rawResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return nil, fmt.Errorf("decode TweetDetail response: %w", err)
	}

	// Navigate: data.threaded_conversation_with_injections_v2.instructions[0].entries
	// Then find the entry matching our tweetID
	article, err := c.extractArticleFromTweetDetail(rawResp, tweetID)
	if err != nil {
		return nil, err
	}

	return article, nil
}

// extractArticleFromTweetDetail extracts article content from the TweetDetail timeline response.
func (c *Client) extractArticleFromTweetDetail(resp map[string]interface{}, targetTweetID string) (*articleContent, error) {
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no data in response")
	}

	// TweetDetail returns data in threaded_conversation_with_injections_v2
	conversation, ok := data["threaded_conversation_with_injections_v2"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no threaded_conversation_with_injections_v2 in response")
	}

	instructions, ok := conversation["instructions"].([]interface{})
	if !ok || len(instructions) == 0 {
		return nil, fmt.Errorf("no instructions in conversation")
	}

	// Find entries in first instruction
	for _, instr := range instructions {
		instrMap, ok := instr.(map[string]interface{})
		if !ok {
			continue
		}

		entries, ok := instrMap["entries"].([]interface{})
		if !ok {
			continue
		}

		// Search for our tweet in entries
		for _, entry := range entries {
			entryMap, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}

			content, ok := entryMap["content"].(map[string]interface{})
			if !ok {
				continue
			}

			// Check itemContent for the tweet
			itemContent, ok := content["itemContent"].(map[string]interface{})
			if !ok {
				continue
			}

			tweetResults, ok := itemContent["tweet_results"].(map[string]interface{})
			if !ok {
				continue
			}

			result, ok := tweetResults["result"].(map[string]interface{})
			if !ok {
				continue
			}

			// Handle TweetWithVisibilityResults wrapper
			if typename, _ := result["__typename"].(string); typename == "TweetWithVisibilityResults" {
				if innerTweet, ok := result["tweet"].(map[string]interface{}); ok {
					result = innerTweet
				}
			}

			// Check if this is our target tweet
			restID, _ := result["rest_id"].(string)
			if restID != targetTweetID {
				continue
			}

			// Found our tweet - look for article data
			article, ok := result["article"].(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("tweet found but no article data")
			}

			return c.parseArticleData(article)
		}
	}

	return nil, fmt.Errorf("tweet %s not found in TweetDetail response", targetTweetID)
}

// parseArticleData parses the article data from the GraphQL response.
func (c *Client) parseArticleData(article map[string]interface{}) (*articleContent, error) {
	articleResults, ok := article["article_results"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no article_results in article")
	}

	result, ok := articleResults["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no result in article_results")
	}

	content := &articleContent{}

	// Extract title
	if title, ok := result["title"].(string); ok {
		content.Title = title
	}

	// Extract content_state blocks to build full article body
	if contentState, ok := result["content_state"].(map[string]interface{}); ok {
		blocks, ok := contentState["blocks"].([]interface{})
		if ok && len(blocks) > 0 {
			var bodyParts []string
			for _, block := range blocks {
				blockMap, ok := block.(map[string]interface{})
				if !ok {
					continue
				}

				text, _ := blockMap["text"].(string)
				blockType, _ := blockMap["type"].(string)

				// Skip empty text unless it's a special block type
				if text == "" && blockType != "atomic" {
					continue
				}

				// Add appropriate formatting based on block type
				switch blockType {
				case "header-one":
					bodyParts = append(bodyParts, "# "+text+"\n")
				case "header-two":
					bodyParts = append(bodyParts, "## "+text+"\n")
				case "header-three":
					bodyParts = append(bodyParts, "### "+text+"\n")
				case "unstyled":
					if text != "" {
						bodyParts = append(bodyParts, text+"\n")
					}
				case "atomic":
					// Atomic blocks are typically media placeholders
					// Add a placeholder that can be replaced with actual image
					bodyParts = append(bodyParts, "[Image]\n")
				default:
					if text != "" {
						bodyParts = append(bodyParts, text+"\n")
					}
				}
			}
			content.Body = strings.TrimSpace(strings.Join(bodyParts, "\n"))
		}
	}

	// Extract media entities as article images
	if mediaEntities, ok := result["media_entities"].([]interface{}); ok {
		for i, media := range mediaEntities {
			mediaMap, ok := media.(map[string]interface{})
			if !ok {
				continue
			}

			imgURL, _ := mediaMap["media_url_https"].(string)
			if imgURL == "" {
				continue
			}

			idStr, _ := mediaMap["id_str"].(string)
			if idStr == "" {
				idStr = fmt.Sprintf("article_img_%d", i)
			}

			var width, height int
			if origInfo, ok := mediaMap["original_info"].(map[string]interface{}); ok {
				if w, ok := origInfo["width"].(float64); ok {
					width = int(w)
				}
				if h, ok := origInfo["height"].(float64); ok {
					height = int(h)
				}
			}

			content.Images = append(content.Images, domain.ArticleImage{
				ID:       idStr,
				URL:      imgURL,
				Width:    width,
				Height:   height,
				Position: i,
			})
		}
	}

	// Also extract cover image if present
	if coverMedia, ok := result["cover_media"].(map[string]interface{}); ok {
		if mediaInfo, ok := coverMedia["media_info"].(map[string]interface{}); ok {
			if imgURL, ok := mediaInfo["original_img_url"].(string); ok && imgURL != "" {
				// Insert cover image at position 0
				coverImg := domain.ArticleImage{
					ID:       "cover",
					URL:      imgURL,
					Position: -1, // Will be sorted to front
				}
				if w, ok := mediaInfo["original_img_width"].(float64); ok {
					coverImg.Width = int(w)
				}
				if h, ok := mediaInfo["original_img_height"].(float64); ok {
					coverImg.Height = int(h)
				}
				content.Images = append([]domain.ArticleImage{coverImg}, content.Images...)
				// Update positions
				for i := range content.Images {
					content.Images[i].Position = i
				}
			}
		}
	}

	if content.Title == "" && content.Body == "" {
		return nil, fmt.Errorf("no article content found")
	}

	c.logger.Info("extracted article from TweetDetail",
		"title", content.Title,
		"body_length", len(content.Body),
		"image_count", len(content.Images),
	)

	return content, nil
}

// ExtractUsernameFromTweetURL extracts the author handle from a tweet URL.
// Example: https://x.com/psyop4921/status/123 -> "psyop4921"
func ExtractUsernameFromTweetURL(url string) string {
	re := regexp.MustCompile(`(?:twitter\.com|x\.com)/([^/]+)/status/\d+`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func extractBitrateFromURL(urlStr string) int {
	// Try to extract bitrate from URL patterns like /vid/avc1/720x1280/... or similar
	re := regexp.MustCompile(`/(\d+)x(\d+)/`)
	matches := re.FindStringSubmatch(urlStr)
	if len(matches) > 2 {
		// Use width * height as a rough bitrate proxy
		var w, h int
		if _, err := fmt.Sscanf(matches[1], "%d", &w); err != nil {
			return 0
		}
		if _, err := fmt.Sscanf(matches[2], "%d", &h); err != nil {
			return 0
		}
		return w * h
	}
	return 0
}

// ============================================================================
// GraphQL API methods for fetching full tweet text (long tweets/notes)
// ============================================================================

// getGuestToken obtains a guest token from X's API.
// Guest tokens are used to access the GraphQL API without user authentication.
func (c *Client) getGuestToken(ctx context.Context) (string, error) {
	c.guestTokenMu.Lock()
	defer c.guestTokenMu.Unlock()

	// Return cached token if still valid (tokens last ~3 hours, we refresh after 1 hour)
	if c.guestToken != "" && time.Now().Before(c.guestTokenExpiry) {
		return c.guestToken, nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.x.com/1.1/guest/activate.json", nil)
	if err != nil {
		return "", fmt.Errorf("create guest token request: %w", err)
	}

	decodedBearer, _ := url.QueryUnescape(bearerToken)
	req.Header.Set("Authorization", "Bearer "+decodedBearer)
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("guest token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("guest token error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		GuestToken string `json:"guest_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode guest token: %w", err)
	}

	c.guestToken = result.GuestToken
	c.guestTokenExpiry = time.Now().Add(1 * time.Hour)

	return c.guestToken, nil
}

// graphQLResponse represents the response from X's GraphQL API.
type graphQLResponse struct {
	Data struct {
		TweetResult struct {
			Result *graphQLTweetResult `json:"result"`
		} `json:"tweetResult"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"errors"`
}

type graphQLTweetResult struct {
	TypeName string `json:"__typename"`
	RestID   string `json:"rest_id"`
	// Tweet field is used when __typename is "TweetWithVisibilityResults" (age-restricted/sensitive content)
	Tweet *graphQLTweetResult `json:"tweet,omitempty"`
	Core  struct {
		UserResults struct {
			Result struct {
				TypeName string `json:"__typename"` // "User", "UserUnavailable", etc.
				ID       string `json:"rest_id"`    // User ID if available
				Reason   string `json:"reason"`     // Reason for UserUnavailable (e.g., "NsfwLoggedOut")
				Message  string `json:"message"`    // Additional message
				Legacy   struct {
					Name            string `json:"name"`
					ScreenName      string `json:"screen_name"`
					ProfileImageURL string `json:"profile_image_url_https"`
					Verified        bool   `json:"verified"`
					FollowersCount  int    `json:"followers_count"`
					FriendsCount    int    `json:"friends_count"`
					StatusesCount   int    `json:"statuses_count"`
					Description     string `json:"description"`
				} `json:"legacy"`
				IsBlueVerified bool `json:"is_blue_verified"`
			} `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
	Legacy struct {
		CreatedAt            string `json:"created_at"`
		FullText             string `json:"full_text"`
		FavoriteCount        int    `json:"favorite_count"`
		RetweetCount         int    `json:"retweet_count"`
		ReplyCount           int    `json:"reply_count"`
		QuoteCount           int    `json:"quote_count"`
		InReplyToStatusIDStr string `json:"in_reply_to_status_id_str"`
		QuotedStatusIDStr    string `json:"quoted_status_id_str"`

		// Media (needed for video/image downloading when syndication API is unavailable)
		Entities struct {
			Media []struct {
				IDStr         string `json:"id_str"`
				Type          string `json:"type"` // "photo", "video", "animated_gif"
				MediaURLHTTPS string `json:"media_url_https"`
				ExtAltText    string `json:"ext_alt_text"`
				Sizes         struct {
					Large struct {
						W int `json:"w"`
						H int `json:"h"`
					} `json:"large"`
				} `json:"sizes"`
			} `json:"media"`
		} `json:"entities"`
		ExtendedEntities struct {
			Media []struct {
				IDStr         string `json:"id_str"`
				Type          string `json:"type"` // "photo", "video", "animated_gif"
				MediaURLHTTPS string `json:"media_url_https"`
				ExtAltText    string `json:"ext_alt_text"`
				Sizes         struct {
					Large struct {
						W int `json:"w"`
						H int `json:"h"`
					} `json:"large"`
				} `json:"sizes"`
				VideoInfo struct {
					DurationMillis int `json:"duration_millis"`
					Variants       []struct {
						Bitrate     int    `json:"bitrate"`
						ContentType string `json:"content_type"`
						URL         string `json:"url"`
					} `json:"variants"`
				} `json:"video_info"`
			} `json:"media"`
		} `json:"extended_entities"`
	} `json:"legacy"`
	NoteTweet struct {
		NoteTweetResults struct {
			Result struct {
				Text         string `json:"text"`
				ID           string `json:"id"`
				EntitySet    struct {
					UserMentions []struct {
						IDStr      string `json:"id_str"`
						ScreenName string `json:"screen_name"`
					} `json:"user_mentions"`
					URLs []struct {
						DisplayURL  string `json:"display_url"`
						ExpandedURL string `json:"expanded_url"`
						URL         string `json:"url"`
					} `json:"urls"`
					Media []struct {
						IDStr         string `json:"id_str"`
						MediaURLHTTPS string `json:"media_url_https"`
						Type          string `json:"type"`
						ExtAltText    string `json:"ext_alt_text"`
						OriginalInfo  struct {
							Width  int `json:"width"`
							Height int `json:"height"`
						} `json:"original_info"`
					} `json:"media"`
				} `json:"entity_set"`
				Richtext struct {
					RichtextTags []struct {
						FromIndex   int      `json:"from_index"`
						ToIndex     int      `json:"to_index"`
						RichtextTypes []string `json:"richtext_types"`
					} `json:"richtext_tags"`
				} `json:"richtext"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
	Views struct {
		Count string `json:"count"`
	} `json:"views"`
	// Article contains article data when this tweet is/contains a long-form article
	Article *graphQLArticle `json:"article,omitempty"`
}

// graphQLArticle represents an X.com article embedded in a tweet
type graphQLArticle struct {
	ArticleResults struct {
		Result *graphQLArticleResult `json:"result"`
	} `json:"article_results"`
}

// graphQLArticleResult contains the actual article content
type graphQLArticleResult struct {
	RestID      string `json:"rest_id"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	PreviewText string `json:"preview_text"`
	CoverMedia  *struct {
		MediaInfo struct {
			OriginalImgURL    string `json:"original_img_url"`
			OriginalImgWidth  int    `json:"original_img_width"`
			OriginalImgHeight int    `json:"original_img_height"`
		} `json:"media_info"`
	} `json:"cover_media"`
	Metadata struct {
		FirstPublishedAtSecs int64 `json:"first_published_at_secs"`
	} `json:"metadata"`
	LifecycleState struct {
		ModifiedAtSecs int64 `json:"modified_at_secs"`
	} `json:"lifecycle_state"`
}

// authSource indicates where authentication came from for logging/debugging.
type authSource string

const (
	authSourceBrowser authSource = "browser"
	authSourceGuest   authSource = "guest"
)

// getGraphQLAuthHeaders returns headers for GraphQL requests.
// Priority: browser credentials (if available) > guest token.
// Returns the headers and the auth source used.
func (c *Client) getGraphQLAuthHeaders(ctx context.Context) (http.Header, authSource, error) {
	// Try browser credentials first
	if browserHeaders := c.getBrowserHeaders(); browserHeaders != nil {
		c.logger.Debug("using browser credentials for GraphQL")
		return browserHeaders, authSourceBrowser, nil
	}

	// Fall back to guest token
	guestToken, err := c.getGuestToken(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get guest token: %w", err)
	}

	decodedBearer, _ := url.QueryUnescape(bearerToken)
	headers := http.Header{
		"Authorization":             []string{"Bearer " + decodedBearer},
		"x-guest-token":             []string{guestToken},
		"User-Agent":                []string{c.userAgent},
		"Content-Type":              []string{"application/json"},
		"x-twitter-active-user":     []string{"yes"},
		"x-twitter-client-language": []string{"en"},
	}

	return headers, authSourceGuest, nil
}

// fetchFullTextFromGraphQL fetches just the full text for a tweet using GraphQL.
// This is used as a fallback when syndication API returns truncated text.
// Automatically refreshes the query ID if it detects a stale ID error.
// Uses browser credentials if available, otherwise falls back to guest token.
func (c *Client) fetchFullTextFromGraphQL(ctx context.Context, tweetID string) (string, error) {
	return c.fetchFullTextFromGraphQLWithRetry(ctx, tweetID, false)
}

func (c *Client) fetchFullTextFromGraphQLWithRetry(ctx context.Context, tweetID string, isRetry bool) (string, error) {
	headers, source, err := c.getGraphQLAuthHeaders(ctx)
	if err != nil {
		return "", err
	}

	queryID, queryIDSource := c.getGraphQLQueryIDWithSource()

	// Build GraphQL request
	variables := fmt.Sprintf(`{"tweetId":"%s","withCommunity":false,"includePromotedContent":false,"withVoice":false}`, tweetID)
	features, featuresSource := c.getGraphQLFeaturesWithSource()

	reqURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/TweetResultByRestId?variables=%s&features=%s",
		queryID,
		url.QueryEscape(variables),
		url.QueryEscape(features))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create graphql request: %w", err)
	}

	// Copy headers to request
	for k, v := range headers {
		req.Header[k] = v
	}
	_ = source // Used for logging if needed

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// If we get a 403, credentials might be stale/rate-limited; clear and retry once
		if resp.StatusCode == http.StatusForbidden && !isRetry {
			c.logger.Warn("GraphQL got 403, clearing credentials and retrying", "tweet_id", tweetID, "auth_source", source)
			if source == authSourceGuest {
				c.guestTokenMu.Lock()
				c.guestToken = ""
				c.guestTokenMu.Unlock()
			}
			// Browser credentials don't need clearing - they'll be refreshed by extension
			return c.fetchFullTextFromGraphQLWithRetry(ctx, tweetID, true)
		}
		return "", fmt.Errorf("graphql error (status %d): %s", resp.StatusCode, string(body))
	}

	var gqlResp graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return "", fmt.Errorf("decode graphql response: %w", err)
	}

	// X's GraphQL can return data AND errors simultaneously. Check for usable data first.
	result := gqlResp.Data.TweetResult.Result
	if result != nil {
		// Prefer note_tweet.text for long-form content
		if result.NoteTweet.NoteTweetResults.Result.Text != "" {
			// High-signal observability: long-form is the primary reason we need GraphQL.
			c.logger.Info("GraphQL note tweet text fetched",
				"tweet_id", tweetID,
				"auth_source", source,
				"query_id_source", queryIDSource,
				"features_source", featuresSource,
			)
			if len(gqlResp.Errors) > 0 {
				c.logger.Debug("GraphQL returned data with non-fatal errors", "tweet_id", tweetID, "error", gqlResp.Errors[0].Message)
			}
			return result.NoteTweet.NoteTweetResults.Result.Text, nil
		}

		// Fall back to legacy full_text
		if result.Legacy.FullText != "" {
			// Lower-signal; keep at debug to avoid spamming logs.
			c.logger.Debug("GraphQL tweet text fetched",
				"tweet_id", tweetID,
				"auth_source", source,
				"query_id_source", queryIDSource,
				"features_source", featuresSource,
			)
			if len(gqlResp.Errors) > 0 {
				c.logger.Debug("GraphQL returned data with non-fatal errors", "tweet_id", tweetID, "error", gqlResp.Errors[0].Message)
			}
			return result.Legacy.FullText, nil
		}
	}

	// No usable data - now check errors for actionable issues
	if len(gqlResp.Errors) > 0 {
		errMsg := gqlResp.Errors[0].Message
		// Check for stale query ID error (only if we didn't get data)
		if strings.Contains(errMsg, "Query") && strings.Contains(errMsg, "Unspecified") && !isRetry && result == nil {
			// Query ID is stale - try to refresh it
			c.logger.Warn("GraphQL query ID appears stale, attempting auto-refresh", "error", errMsg, "old_query_id", queryID)
			c.clearGraphQLQueryID() // Clear the cached ID first

			newQueryID, refreshErr := c.refreshGraphQLQueryID(ctx)
			if refreshErr != nil {
				c.logger.Error("failed to refresh GraphQL query ID", "error", refreshErr)
				return "", fmt.Errorf("graphql API error (stale query ID, refresh failed: %v): %s", refreshErr, errMsg)
			}

			c.logger.Info("retrying GraphQL request with new query ID", "new_query_id", newQueryID)
			return c.fetchFullTextFromGraphQLWithRetry(ctx, tweetID, true)
		}
		return "", fmt.Errorf("graphql API error: %s", errMsg)
	}

	return "", fmt.Errorf("no text found in graphql response")
}

// fetchFromGraphQL fetches complete tweet data using X's GraphQL API.
// This is used as a fallback when the syndication API fails entirely.
// Uses browser credentials if available, otherwise falls back to guest token.
func (c *Client) fetchFromGraphQL(ctx context.Context, tweetID string) (*domain.Tweet, error) {
	return c.fetchFromGraphQLWithRetry(ctx, tweetID, false)
}

func (c *Client) fetchFromGraphQLWithRetry(ctx context.Context, tweetID string, isRetry bool) (*domain.Tweet, error) {
	headers, source, err := c.getGraphQLAuthHeaders(ctx)
	if err != nil {
		return nil, err
	}

	queryID, queryIDSource := c.getGraphQLQueryIDWithSource()

	// Build GraphQL request
	variables := fmt.Sprintf(`{"tweetId":"%s","withCommunity":false,"includePromotedContent":false,"withVoice":false}`, tweetID)
	features, featuresSource := c.getGraphQLFeaturesWithSource()

	reqURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/TweetResultByRestId?variables=%s&features=%s",
		queryID,
		url.QueryEscape(variables),
		url.QueryEscape(features))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create graphql request: %w", err)
	}

	// Copy headers to request
	for k, v := range headers {
		req.Header[k] = v
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// If we get a 403, credentials might be stale/rate-limited; clear and retry once
		if resp.StatusCode == http.StatusForbidden && !isRetry {
			c.logger.Warn("GraphQL got 403, clearing credentials and retrying", "tweet_id", tweetID, "auth_source", source)
			if source == authSourceGuest {
				c.guestTokenMu.Lock()
				c.guestToken = ""
				c.guestTokenMu.Unlock()
			}
			return c.fetchFromGraphQLWithRetry(ctx, tweetID, true)
		}
		return nil, fmt.Errorf("graphql error (status %d): %s", resp.StatusCode, string(body))
	}

	var gqlResp graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql API error: %s", gqlResp.Errors[0].Message)
	}

	// Observability: this path is only used when syndication fails entirely.
	c.logger.Info("GraphQL tweet fetch succeeded",
		"tweet_id", tweetID,
		"auth_source", source,
		"query_id_source", queryIDSource,
		"features_source", featuresSource,
	)

	tweet, err := c.parseGraphQLResponse(tweetID, &gqlResp)
	if err != nil {
		return nil, err
	}

	// If this is an article, fetch full content via TweetDetail
	if tweet.ContentType == domain.ContentTypeArticle {
		c.enrichArticleContent(ctx, tweet)
	}

	return tweet, nil
}

func (c *Client) parseGraphQLResponse(tweetID string, resp *graphQLResponse) (*domain.Tweet, error) {
	result := resp.Data.TweetResult.Result
	if result == nil {
		return nil, fmt.Errorf("no tweet result in response")
	}

	// Handle "TweetTombstone" (deleted) or other non-tweet types
	if result.TypeName == "TweetTombstone" {
		return nil, fmt.Errorf("tweet is unavailable (deleted or protected)")
	}

	// Handle "TweetWithVisibilityResults" - age-restricted/sensitive content wrapper
	// The actual tweet data is nested inside the "tweet" field
	if result.TypeName == "TweetWithVisibilityResults" {
		if result.Tweet != nil {
			result = result.Tweet
		} else {
			return nil, fmt.Errorf("age-restricted tweet has no nested tweet data (authentication may be required)")
		}
	}

	// Handle missing user data - try to fetch separately if we have user ID
	if result.Core.UserResults.Result.Legacy.ScreenName == "" {
		userID := result.Core.UserResults.Result.ID
		c.logger.Warn("tweet author data missing, attempting user lookup",
			"tweet_id", tweetID,
			"type_name", result.TypeName,
			"has_legacy", result.Legacy.FullText != "",
			"core_type", result.Core.UserResults.Result.TypeName,
			"user_id", userID,
		)

		// If we have a user ID, try to fetch user data separately
		if userID != "" {
			userData, err := c.fetchUserByRestID(context.Background(), userID)
			if err != nil {
				c.logger.Warn("failed to fetch user by rest_id", "user_id", userID, "error", err)
			} else if userData != nil {
				// Successfully fetched user data - update the result
				result.Core.UserResults.Result.Legacy = *userData
				c.logger.Info("successfully fetched missing user data", "user_id", userID, "screen_name", userData.ScreenName)
			}
		}

		// If still no screen_name, fail
		//
		// Important: do NOT hard-fail the entire tweet archive if author profile fields
		// are unavailable. This happens for NSFW/age-restricted tweets, user privacy
		// settings, and when Cloudflare blocks user lookup from the backend.
		//
		// We keep the tweet content and set a stable placeholder author so the archive
		// remains usable and can be re-synced later if richer author data becomes available.
		if result.Core.UserResults.Result.Legacy.ScreenName == "" {
			placeholder := "unknown"
			if userID != "" {
				placeholder = "user_" + userID
			}
			c.logger.Warn("author data still unavailable; using placeholder author",
				"tweet_id", tweetID,
				"user_id", userID,
				"placeholder_username", placeholder,
				"core_type", result.Core.UserResults.Result.TypeName,
				"reason", result.Core.UserResults.Result.Reason,
			)
			result.Core.UserResults.Result.Legacy.ScreenName = placeholder
			if result.Core.UserResults.Result.Legacy.Name == "" {
				result.Core.UserResults.Result.Legacy.Name = placeholder
			}
		}
	}

	// Parse created_at
	postedAt, _ := time.Parse(time.RubyDate, result.Legacy.CreatedAt)
	if postedAt.IsZero() {
		postedAt = time.Now()
	}

	// Get text - prefer note_tweet for long-form
	text := result.Legacy.FullText
	if result.NoteTweet.NoteTweetResults.Result.Text != "" {
		text = result.NoteTweet.NoteTweetResults.Result.Text
	}

	user := result.Core.UserResults.Result

	tweet := &domain.Tweet{
		ID:       domain.TweetID(tweetID),
		Text:     text,
		PostedAt: postedAt,
		Author: domain.Author{
			ID:             user.ID,
			Username:       user.Legacy.ScreenName,
			DisplayName:    user.Legacy.Name,
			AvatarURL:      user.Legacy.ProfileImageURL,
			Verified:       user.Legacy.Verified || user.IsBlueVerified,
			FollowerCount:  user.Legacy.FollowersCount,
			FollowingCount: user.Legacy.FriendsCount,
			TweetCount:     user.Legacy.StatusesCount,
			Description:    user.Legacy.Description,
		},
		Metrics: domain.TweetMetrics{
			Likes:    result.Legacy.FavoriteCount,
			Retweets: result.Legacy.RetweetCount,
			Replies:  result.Legacy.ReplyCount,
			Quotes:   result.Legacy.QuoteCount,
		},
		Status:    domain.ArchiveStatusPending,
		CreatedAt: time.Now(),
	}

	// Parse media from GraphQL legacy entities. This is crucial for NSFW/visibility-wrapped
	// tweets where the syndication API often fails, but GraphQL succeeds.
	tweet.Media = c.parseGraphQLMedia(result)

	// Parse view count if available
	if result.Views.Count != "" {
		var views int
		if _, err := fmt.Sscanf(result.Views.Count, "%d", &views); err == nil {
			tweet.Metrics.Views = views
		}
	}

	// Set reply/quote references
	if result.Legacy.InReplyToStatusIDStr != "" {
		replyTo := domain.TweetID(result.Legacy.InReplyToStatusIDStr)
		tweet.ReplyTo = &replyTo
	}
	if result.Legacy.QuotedStatusIDStr != "" {
		quoted := domain.TweetID(result.Legacy.QuotedStatusIDStr)
		tweet.QuotedTweet = &quoted
	}

	// Extract article data if present
	c.extractArticleFromGraphQL(result, tweet)

	return tweet, nil
}

// extractArticleFromGraphQL populates article fields from GraphQL response.
func (c *Client) extractArticleFromGraphQL(result *graphQLTweetResult, tweet *domain.Tweet) {
	if result.Article == nil {
		return
	}

	articleResult := result.Article.ArticleResults.Result
	if articleResult == nil {
		return
	}

	// This is an actual X Article - mark it as such
	tweet.ContentType = domain.ContentTypeArticle
	tweet.ArticleTitle = articleResult.Title

	// Use preview text as body if we don't have full content
	// The full article body is typically in the note_tweet text for long-form content
	if articleResult.PreviewText != "" && tweet.ArticleBody == "" {
		tweet.ArticleBody = articleResult.PreviewText
	}

	// If the main tweet text is longer than preview, use that as the full article body
	if len(tweet.Text) > len(articleResult.PreviewText) {
		tweet.ArticleBody = tweet.Text
	}

	// Calculate word count and reading time
	if tweet.ArticleBody != "" {
		words := strings.Fields(tweet.ArticleBody)
		tweet.WordCount = len(words)
		tweet.ReadingMinutes = tweet.CalculateReadingTime()
	}

	// Extract cover image as an article image
	if articleResult.CoverMedia != nil && articleResult.CoverMedia.MediaInfo.OriginalImgURL != "" {
		coverImage := domain.ArticleImage{
			ID:       articleResult.ID + "_cover",
			URL:      articleResult.CoverMedia.MediaInfo.OriginalImgURL,
			Width:    articleResult.CoverMedia.MediaInfo.OriginalImgWidth,
			Height:   articleResult.CoverMedia.MediaInfo.OriginalImgHeight,
			Position: 0, // Cover image is first
		}
		tweet.ArticleImages = append(tweet.ArticleImages, coverImage)
	}

	c.logger.Info("extracted article from GraphQL",
		"tweet_id", tweet.ID,
		"article_id", articleResult.RestID,
		"title", articleResult.Title,
		"word_count", tweet.WordCount,
		"reading_minutes", tweet.ReadingMinutes,
		"has_cover_image", articleResult.CoverMedia != nil,
	)
}

// parseGraphQLMedia extracts media (images/videos) from GraphQL legacy entities.
// For videos/GIFs, it selects the best MP4 variant by bitrate.
func (c *Client) parseGraphQLMedia(result *graphQLTweetResult) []domain.Media {
	if result == nil {
		return nil
	}

	var out []domain.Media
	seen := map[string]bool{}

	// Prefer extended_entities because it includes video_info for videos.
	for _, m := range result.Legacy.ExtendedEntities.Media {
		switch m.Type {
		case "photo":
			if m.MediaURLHTTPS == "" || seen[m.MediaURLHTTPS] {
				continue
			}
			seen[m.MediaURLHTTPS] = true
			id := m.IDStr
			if id == "" {
				id = fmt.Sprintf("photo_%d", len(out))
			}
			out = append(out, domain.Media{
				ID:      id,
				Type:    domain.MediaTypeImage,
				URL:     m.MediaURLHTTPS,
				PreviewURL: "",
				Width:   m.Sizes.Large.W,
				Height:  m.Sizes.Large.H,
				AltText: m.ExtAltText,
			})

		case "video", "animated_gif":
			mediaType := domain.MediaTypeVideo
			if m.Type == "animated_gif" {
				mediaType = domain.MediaTypeGIF
			}

			bestURL := ""
			bestBitrate := -1
			for _, v := range m.VideoInfo.Variants {
				// Ignore HLS playlists; we want a direct MP4 for download.
				if v.ContentType != "video/mp4" || v.URL == "" {
					continue
				}
				b := v.Bitrate
				if b == 0 {
					// Some variants don't include bitrate; infer from URL as a fallback.
					b = extractBitrateFromURL(v.URL)
				}
				if b > bestBitrate {
					bestBitrate = b
					bestURL = v.URL
				}
			}
			if bestURL == "" {
				continue
			}

			id := m.IDStr
			if id == "" {
				id = "video_" + fmt.Sprintf("%d", len(out))
			}
			out = append(out, domain.Media{
				ID:         id,
				Type:       mediaType,
				URL:        bestURL,
				PreviewURL: m.MediaURLHTTPS, // thumbnail/poster
				Width:      m.Sizes.Large.W,
				Height:     m.Sizes.Large.H,
				Duration:   m.VideoInfo.DurationMillis / 1000,
				Bitrate:    bestBitrate,
				AltText:    m.ExtAltText,
			})
		}
	}

	// If extended_entities had nothing, fall back to entities (usually photos).
	if len(out) == 0 {
		for _, m := range result.Legacy.Entities.Media {
			if m.Type != "photo" {
				continue
			}
			if m.MediaURLHTTPS == "" || seen[m.MediaURLHTTPS] {
				continue
			}
			seen[m.MediaURLHTTPS] = true
			id := m.IDStr
			if id == "" {
				id = fmt.Sprintf("photo_%d", len(out))
			}
			out = append(out, domain.Media{
				ID:      id,
				Type:    domain.MediaTypeImage,
				URL:     m.MediaURLHTTPS,
				Width:   m.Sizes.Large.W,
				Height:  m.Sizes.Large.H,
				AltText: m.ExtAltText,
			})
		}
	}

	return out
}

// userLegacyData holds the user profile fields we need
type userLegacyData struct {
	Name            string `json:"name"`
	ScreenName      string `json:"screen_name"`
	ProfileImageURL string `json:"profile_image_url_https"`
	Verified        bool   `json:"verified"`
	FollowersCount  int    `json:"followers_count"`
	FriendsCount    int    `json:"friends_count"`
	StatusesCount   int    `json:"statuses_count"`
	Description     string `json:"description"`
}

// userByRestIDResponse is the GraphQL response for UserByRestId
type userByRestIDResponse struct {
	Data struct {
		// For UsersByRestIds (plural) endpoint
		Users []struct {
			Result struct {
				TypeName       string         `json:"__typename"`
				ID             string         `json:"rest_id"`
				Legacy         userLegacyData `json:"legacy"`
				IsBlueVerified bool           `json:"is_blue_verified"`
			} `json:"result"`
		} `json:"users"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// userByRestIDFeatures contains the required feature flags for UserByRestId endpoint
const userByRestIDFeatures = `{"hidden_profile_subscriptions_enabled":true,"rweb_tipjar_consumption_enabled":true,"responsive_web_graphql_exclude_directive_enabled":true,"verified_phone_label_enabled":false,"subscriptions_verification_info_is_identity_verified_enabled":true,"subscriptions_verification_info_verified_since_enabled":true,"highlights_tweets_tab_ui_enabled":true,"responsive_web_twitter_article_notes_tab_enabled":true,"subscriptions_feature_can_gift_premium":true,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"responsive_web_graphql_timeline_navigation_enabled":true}`

// fetchUserByRestID fetches user data by their REST ID using GraphQL.
// This is used as a fallback when tweet data is returned without user profile info.
func (c *Client) fetchUserByRestID(ctx context.Context, userID string) (*userLegacyData, error) {
	// Use browser credentials for authenticated access
	headers := c.getBrowserHeaders()
	if headers == nil {
		return nil, fmt.Errorf("browser credentials not available for user lookup")
	}

	// Build UsersByRestIds GraphQL request (plural endpoint)
	// Try to get query ID from browser capture, fall back to known value
	queryID := c.getBrowserQueryID("UsersByRestIds")
	if queryID == "" {
		// Try singular as fallback
		queryID = c.getBrowserQueryID("UserByRestId")
	}
	if queryID == "" {
		queryID = "OJnDIdHX7gWdPjVT7dlZUg" // Fallback query ID for UsersByRestIds
	}

	// Use array format for UsersByRestIds endpoint
	variables := fmt.Sprintf(`{"userIds":["%s"]}`, userID)
	// Use specific feature flags for UsersByRestIds
	features := json.RawMessage(userByRestIDFeatures)

	reqURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/UsersByRestIds?variables=%s&features=%s",
		queryID,
		url.QueryEscape(variables),
		url.QueryEscape(string(features)),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers from browser credentials
	for key, values := range headers {
		for _, value := range values {
			req.Header.Set(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("user lookup failed (status %d): %s", resp.StatusCode, string(body))
	}

	var userResp userByRestIDResponse
	if err := json.NewDecoder(resp.Body).Decode(&userResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(userResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", userResp.Errors[0].Message)
	}

	// UsersByRestIds returns an array
	if len(userResp.Data.Users) == 0 {
		return nil, fmt.Errorf("no users in response")
	}

	result := userResp.Data.Users[0].Result
	if result.TypeName == "UserUnavailable" {
		return nil, fmt.Errorf("user unavailable")
	}

	if result.Legacy.ScreenName == "" {
		return nil, fmt.Errorf("user data empty in response")
	}

	c.logger.Info("fetched user by rest_id",
		"user_id", userID,
		"screen_name", result.Legacy.ScreenName,
		"name", result.Legacy.Name,
	)

	return &result.Legacy, nil
}

// TestUserLookupResult contains the result of a user lookup test.
type TestUserLookupResult struct {
	Success    bool   `json:"success"`
	UserID     string `json:"user_id"`
	ScreenName string `json:"screen_name,omitempty"`
	Name       string `json:"name,omitempty"`
	Error      string `json:"error,omitempty"`
}

// TestUserLookup tests the UserByRestId endpoint with the current browser credentials.
// This is a debug endpoint to verify the fix works before deploying.
func (c *Client) TestUserLookup(ctx context.Context, userID string) TestUserLookupResult {
	result := TestUserLookupResult{UserID: userID}

	userData, err := c.fetchUserByRestID(ctx, userID)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.ScreenName = userData.ScreenName
	result.Name = userData.Name
	return result
}
