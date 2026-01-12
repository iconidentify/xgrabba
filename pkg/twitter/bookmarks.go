package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RateLimitError indicates the request hit a rate limit and includes a reset time if known.
type RateLimitError struct {
	Reset time.Time
}

func (e *RateLimitError) Error() string {
	if !e.Reset.IsZero() {
		return fmt.Sprintf("rate limited until %s", e.Reset.Format(time.RFC3339))
	}
	return "rate limited"
}

// BookmarksClient fetches bookmark lists from X API v2.
type BookmarksClient struct {
	httpClient  *http.Client
	baseURL     string
	bearerToken string
	userAgent   string
}

type BookmarksClientConfig struct {
	BaseURL     string
	BearerToken string
	Timeout     time.Duration
	UserAgent   string
}

func NewBookmarksClient(cfg BookmarksClientConfig) *BookmarksClient {
	base := cfg.BaseURL
	if base == "" {
		base = "https://api.x.com/2"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "xgrabba-bookmarks-monitor/1.0"
	}
	return &BookmarksClient{
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		baseURL:     strings.TrimRight(base, "/"),
		bearerToken: cfg.BearerToken,
		userAgent:   ua,
	}
}

type listBookmarksResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Meta struct {
		NextToken string `json:"next_token"`
	} `json:"meta"`
}

// ListBookmarks returns bookmark tweet IDs for a user (most recent first).
func (c *BookmarksClient) ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) (ids []string, nextToken string, err error) {
	if userID == "" {
		return nil, "", fmt.Errorf("userID is required")
	}
	if c.bearerToken == "" {
		return nil, "", fmt.Errorf("bearer token is required")
	}
	if maxResults <= 0 {
		maxResults = 100
	}
	if maxResults > 100 {
		maxResults = 100
	}

	u, err := url.Parse(c.baseURL + "/users/" + userID + "/bookmarks")
	if err != nil {
		return nil, "", fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	q.Set("max_results", strconv.Itoa(maxResults))
	// Keep response minimal; we only need IDs.
	if paginationToken != "" {
		q.Set("pagination_token", paginationToken)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		rl := &RateLimitError{}
		if reset := resp.Header.Get("x-rate-limit-reset"); reset != "" {
			if sec, err := strconv.ParseInt(reset, 10, 64); err == nil {
				rl.Reset = time.Unix(sec, 0)
			}
		}
		return nil, "", rl
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Avoid huge reads; just return status.
		return nil, "", fmt.Errorf("bookmarks API error: %s", resp.Status)
	}

	var parsed listBookmarksResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	out := make([]string, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out, parsed.Meta.NextToken, nil
}

