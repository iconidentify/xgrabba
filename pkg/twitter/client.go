package twitter

import (
	"context"
	"encoding/json"
	"fmt"
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
	defaultTweetResultByRestIDQueryID = "geNbknbFuVk6S2dpb8lr2Q"
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
	if ff := c.getBrowserFeatureFlags(); len(ff) > 0 {
		return string(ff), "browser"
	}
	return defaultGraphQLFeatures, "default"
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

		// For note tweets (long-form), syndication text is definitely truncated.
		// We MUST fetch full text via GraphQL. For regular tweets, try GraphQL but don't require it.
		fullText, gqlErr := c.fetchFullTextFromGraphQL(ctx, tweetID)

		if gqlErr == nil && fullText != "" {
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

		return tweet, nil
	}

	// If syndication fails, try GraphQL directly
	tweet, graphqlErr := c.fetchFromGraphQL(ctx, tweetID)
	if graphqlErr == nil {
		tweet.URL = tweetURL
		return tweet, nil
	}

	return nil, fmt.Errorf("failed to fetch tweet (syndication: %v, graphql: %v)", err, graphqlErr)
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
	Tweet      *domain.Tweet
	IsNoteTweet bool // True if this is a long-form tweet (note_tweet field present)
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
				Legacy struct {
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
	} `json:"legacy"`
	NoteTweet struct {
		NoteTweetResults struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
	Views struct {
		Count string `json:"count"`
	} `json:"views"`
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

	return c.parseGraphQLResponse(tweetID, &gqlResp)
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

	// Log the result type for debugging visibility issues
	if result.Core.UserResults.Result.Legacy.ScreenName == "" {
		return nil, fmt.Errorf("tweet author data unavailable (type=%s, has_core=%v)", result.TypeName, result.Core.UserResults.Result.Legacy.Name != "")
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

	// Note: Media parsing from GraphQL is more complex and would require additional work
	// For now, we rely on syndication API for media when possible

	return tweet, nil
}
