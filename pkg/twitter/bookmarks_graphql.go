package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Default query id as a last-resort fallback. Prefer browser-captured query_ids via extension.
// This value may go stale at any time.
const defaultBookmarksQueryID = "sLg287PtRrRWcUciNGFufQ"

// GraphQLBookmarksClient fetches bookmark tweet IDs via X's internal GraphQL endpoints.
// This requires browser session credentials (auth_token + ct0) captured by the extension.
type GraphQLBookmarksClient struct {
	c *Client
}

func NewGraphQLBookmarksClient(c *Client) *GraphQLBookmarksClient {
	return &GraphQLBookmarksClient{c: c}
}

// ListBookmarks returns bookmark tweet IDs for a user (most recent first).
// userID is ignored for GraphQL mode (session determines the user).
func (b *GraphQLBookmarksClient) ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) (ids []string, nextToken string, err error) {
	if b == nil || b.c == nil {
		return nil, "", fmt.Errorf("client is nil")
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	if maxResults > 100 {
		maxResults = 100
	}

	// Bookmarks GraphQL requires browser session cookies; do not fall back to guest.
	headers := b.c.getBrowserHeaders()
	if headers == nil {
		return nil, "", fmt.Errorf("browser credentials not available (enable forwarding in extension and visit x.com)")
	}

	queryID := b.c.getBrowserQueryID("Bookmarks")
	if queryID == "" {
		queryID = defaultBookmarksQueryID
	}

	vars := map[string]any{
		"count":                 maxResults,
		"includePromotedContent": false,
	}
	if strings.TrimSpace(paginationToken) != "" {
		vars["cursor"] = paginationToken
	}
	varsJSON, _ := json.Marshal(vars)

	features := b.c.getGraphQLFeatures()

	reqURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/Bookmarks?variables=%s&features=%s",
		queryID,
		url.QueryEscape(string(varsJSON)),
		url.QueryEscape(features),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header[k] = v
	}

	resp, err := b.c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		rl := &RateLimitError{}
		if reset := resp.Header.Get("x-rate-limit-reset"); reset != "" {
			// X sometimes provides unix seconds.
			if sec, parseErr := parseUnixSeconds(reset); parseErr == nil {
				rl.Reset = time.Unix(sec, 0)
			}
		}
		return nil, "", rl
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("bookmarks graphql unauthorized: %s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("bookmarks graphql error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	idSet := map[string]struct{}{}
	extractTweetIDsFromBookmarksResponse(parsed, idSet)
	ids = make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] }) // best-effort "recent first"

	nextToken = extractBottomCursor(parsed)
	return ids, nextToken, nil
}

func extractTweetIDsFromBookmarksResponse(v any, out map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		// Preferred pattern: ... tweet_results: { result: { rest_id: "..." } }
		if tr, ok := t["tweet_results"].(map[string]any); ok {
			if res, ok := tr["result"].(map[string]any); ok {
				if rid, ok := res["rest_id"].(string); ok && rid != "" {
					out[rid] = struct{}{}
				}
			}
		}
		for _, vv := range t {
			extractTweetIDsFromBookmarksResponse(vv, out)
		}
	case []any:
		for _, vv := range t {
			extractTweetIDsFromBookmarksResponse(vv, out)
		}
	}
}

func extractBottomCursor(v any) string {
	var cursor string
	var walk func(any)
	walk = func(x any) {
		if cursor != "" {
			return
		}
		switch t := x.(type) {
		case map[string]any:
			if ct, ok := t["cursorType"].(string); ok && (ct == "Bottom" || ct == "ShowMore") {
				if val, ok := t["value"].(string); ok && val != "" {
					cursor = val
					return
				}
			}
			for _, vv := range t {
				walk(vv)
			}
		case []any:
			for _, vv := range t {
				walk(vv)
			}
		}
	}
	walk(v)
	return cursor
}

func parseUnixSeconds(s string) (int64, error) {
	// avoid pulling strconv in a helper file? keep simple with fmt.
	var sec int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &sec)
	if err != nil {
		return 0, err
	}
	return sec, nil
}

