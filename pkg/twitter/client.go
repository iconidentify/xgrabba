package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

// Client fetches tweet data from X.com.
type Client struct {
	httpClient *http.Client
	userAgent  string
}

// NewClient creates a new Twitter client.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

// FetchTweet retrieves tweet data from X.com.
func (c *Client) FetchTweet(ctx context.Context, tweetURL string) (*domain.Tweet, error) {
	tweetID := ExtractTweetID(tweetURL)
	if tweetID == "" {
		return nil, fmt.Errorf("could not extract tweet ID from URL: %s", tweetURL)
	}

	// Try syndication API (works for public tweets)
	tweet, err := c.fetchFromSyndication(ctx, tweetID)
	if err == nil {
		tweet.URL = tweetURL
		return tweet, nil
	}

	return nil, fmt.Errorf("failed to fetch tweet: %w", err)
}

// fetchFromSyndication uses Twitter's public syndication API.
func (c *Client) fetchFromSyndication(ctx context.Context, tweetID string) (*domain.Tweet, error) {
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

	return c.parseSyndicationResponse(tweetID, &syndicationResp)
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
}

func (c *Client) parseSyndicationResponse(tweetID string, resp *syndicationResponse) (*domain.Tweet, error) {
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

func extractBitrateFromURL(url string) int {
	// Try to extract bitrate from URL patterns like /vid/avc1/720x1280/... or similar
	re := regexp.MustCompile(`/(\d+)x(\d+)/`)
	matches := re.FindStringSubmatch(url)
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
