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

// HasBrowserCredentials reports whether the wrapped twitter client currently has valid browser credentials.
// Used by the bookmarks monitor to trigger an immediate poll when credentials first appear.
func (b *GraphQLBookmarksClient) HasBrowserCredentials() bool {
	if b == nil || b.c == nil {
		return false
	}
	return b.c.HasBrowserCredentials()
}

func (b *GraphQLBookmarksClient) getBookmarksFeatures() string {
	// X's Bookmarks GraphQL endpoint is strict about certain feature flags not being null.
	// We defensively ensure they're present and boolean.
	//
	// Important: using only browser-observed feature flags can omit required keys, which X treats as null.
	// So we merge: default feature set (base) + browser-observed overrides.
	requiredTrue := []string{
		"responsive_web_media_download_video_enabled",
		"graphql_timeline_v2_bookmark_timeline",
		"responsive_web_graphql_exclude_directive_enabled",
		"tweetypie_unmention_optimization_enabled",
		"responsive_web_home_pinned_timelines_enabled",
	}

	loadMap := func(raw []byte) (map[string]any, bool) {
		if len(raw) == 0 {
			return nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m == nil {
			return nil, false
		}
		return m, true
	}

	// Base defaults
	m, _ := loadMap([]byte(defaultGraphQLFeatures))
	if m == nil {
		m = map[string]any{}
	}

	// Overlay browser flags (if available)
	if ff := b.c.getBrowserFeatureFlags(); len(ff) > 0 {
		if browserM, ok := loadMap(ff); ok {
			for k, v := range browserM {
				m[k] = v
			}
		}
	}

	for _, k := range requiredTrue {
		v, ok := m[k]
		if !ok || v == nil {
			m[k] = true
			continue
		}
		// If the extension provided a non-bool (unlikely), force true to satisfy server validation.
		if _, ok := v.(bool); !ok {
			m[k] = true
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		// Last resort: send required keys only.
		fallback := map[string]any{}
		for _, k := range requiredTrue {
			fallback[k] = true
		}
		out2, _ := json.Marshal(fallback)
		return string(out2)
	}
	return string(out)
}

// ListBookmarks returns bookmark tweet IDs for a user (most recent first).
// userID is ignored for GraphQL mode (session determines the user).
func (b *GraphQLBookmarksClient) ListBookmarks(ctx context.Context, userID string, maxResults int, paginationToken string) (ids []string, nextToken string, err error) {
	return b.listBookmarksWithRetry(ctx, userID, maxResults, paginationToken, false)
}

func (b *GraphQLBookmarksClient) listBookmarksWithRetry(ctx context.Context, userID string, maxResults int, paginationToken string, isRetry bool) (ids []string, nextToken string, err error) {
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

	queryID, queryIDSource := b.c.getBookmarksQueryIDWithSource()

	vars := map[string]any{
		"count":                 maxResults,
		"includePromotedContent": false,
	}
	if strings.TrimSpace(paginationToken) != "" {
		vars["cursor"] = paginationToken
	}
	varsJSON, _ := json.Marshal(vars)

	features := b.getBookmarksFeatures()

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
		// If we got a 400 while using a default/cached query ID, it may have gone stale.
		// Try refreshing from main.js once.
		if resp.StatusCode == http.StatusBadRequest && !isRetry && (queryIDSource == "default" || queryIDSource == "cached") {
			if _, refreshErr := b.c.refreshBookmarksQueryID(ctx); refreshErr == nil {
				return b.listBookmarksWithRetry(ctx, userID, maxResults, paginationToken, true)
			}
		}
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

