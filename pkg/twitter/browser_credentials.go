package twitter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// BrowserCredentials holds authentication credentials captured from a logged-in browser session.
// These credentials enable server-side GraphQL access using the user's session.
type BrowserCredentials struct {
	AuthToken    string            `json:"auth_token"`     // Session authentication token
	CT0          string            `json:"ct0"`            // CSRF token
	QueryIDs     map[string]string `json:"query_ids"`      // GraphQL endpoint -> queryId mappings
	FeatureFlags json.RawMessage   `json:"feature_flags"`  // Current feature flags
	UpdatedAt    time.Time         `json:"updated_at"`     // When credentials were last updated
	ExpiresAt    time.Time         `json:"expires_at"`     // When credentials should be considered stale
}

// IsValid checks if the credentials are present and not expired.
func (bc *BrowserCredentials) IsValid() bool {
	if bc == nil || bc.AuthToken == "" || bc.CT0 == "" {
		return false
	}
	return time.Now().Before(bc.ExpiresAt)
}

// browserCredsStore manages browser credentials with thread-safe access.
type browserCredsStore struct {
	creds *BrowserCredentials
	mu    sync.RWMutex
}

var credsStore = &browserCredsStore{}

// SetBrowserCredentials stores browser credentials for use in GraphQL calls.
func (c *Client) SetBrowserCredentials(creds BrowserCredentials) {
	credsStore.mu.Lock()
	defer credsStore.mu.Unlock()

	// Set expiry to 1 hour from now (credentials refresh on each page load)
	if creds.ExpiresAt.IsZero() {
		creds.ExpiresAt = time.Now().Add(1 * time.Hour)
	}
	if creds.UpdatedAt.IsZero() {
		creds.UpdatedAt = time.Now()
	}
	credsStore.creds = &creds

	// Sandbox-level observability (no secrets):
	// - whether query IDs / feature flags were included
	// - how many operations were provided
	queryIDCount := 0
	queryIDKeys := make([]string, 0, 8)
	for k := range creds.QueryIDs {
		queryIDCount++
		if len(queryIDKeys) < 8 {
			queryIDKeys = append(queryIDKeys, k)
		}
	}
	sort.Strings(queryIDKeys)
	ffBytes := len(creds.FeatureFlags)
	hasFF := ffBytes > 0
	c.logger.Info("browser credentials updated",
		"has_query_ids", queryIDCount > 0,
		"query_id_count", queryIDCount,
		"query_id_keys_sample", queryIDKeys,
		"has_feature_flags", hasFF,
		"feature_flags_bytes", ffBytes,
	)
}

// GetBrowserCredentials returns the current browser credentials.
func (c *Client) GetBrowserCredentials() *BrowserCredentials {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()
	return credsStore.creds
}

// HasBrowserCredentials checks if valid browser credentials are available.
func (c *Client) HasBrowserCredentials() bool {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()
	return credsStore.creds != nil && credsStore.creds.IsValid()
}

// ClearBrowserCredentials removes stored browser credentials.
func (c *Client) ClearBrowserCredentials() {
	credsStore.mu.Lock()
	defer credsStore.mu.Unlock()
	credsStore.creds = nil
}

// getBrowserHeaders returns HTTP headers for authenticated GraphQL requests.
// Returns nil if browser credentials are not available.
func (c *Client) getBrowserHeaders() http.Header {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()

	if credsStore.creds == nil || !credsStore.creds.IsValid() {
		return nil
	}

	decodedBearer, _ := url.QueryUnescape(bearerToken)

	return http.Header{
		"Authorization":              []string{"Bearer " + decodedBearer},
		"x-csrf-token":               []string{credsStore.creds.CT0},
		"Cookie":                     []string{"auth_token=" + credsStore.creds.AuthToken + "; ct0=" + credsStore.creds.CT0},
		"User-Agent":                 []string{c.userAgent},
		"Content-Type":               []string{"application/json"},
		"x-twitter-active-user":      []string{"yes"},
		"x-twitter-client-language":  []string{"en"},
		"x-twitter-auth-type":        []string{"OAuth2Session"},
	}
}

// getBrowserQueryID returns a query ID for an operation (e.g. "TweetResultByRestId", "Bookmarks")
// when browser credentials are available and include query_ids.
func (c *Client) getBrowserQueryID(operation string) string {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()

	if credsStore.creds == nil || !credsStore.creds.IsValid() {
		return ""
	}
	if credsStore.creds.QueryIDs == nil {
		return ""
	}
	return credsStore.creds.QueryIDs[operation]
}

// getBrowserFeatureFlags returns the last observed GraphQL "features" JSON (decoded),
// or nil if unavailable.
func (c *Client) getBrowserFeatureFlags() json.RawMessage {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()

	if credsStore.creds == nil || !credsStore.creds.IsValid() {
		return nil
	}
	if len(credsStore.creds.FeatureFlags) == 0 {
		return nil
	}
	return credsStore.creds.FeatureFlags
}

// BrowserCredentialsStatus returns information about the current credential state.
type BrowserCredentialsStatus struct {
	HasCredentials bool       `json:"has_credentials"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	IsExpired      bool       `json:"is_expired"`
}

// BrowserCredentialsDebugStatus adds non-secret debug info to help validate that
// query_ids and feature_flags are arriving from the browser.
type BrowserCredentialsDebugStatus struct {
	BrowserCredentialsStatus
	HasQueryIDs       bool     `json:"has_query_ids"`
	QueryIDCount      int      `json:"query_id_count"`
	QueryIDKeysSample []string `json:"query_id_keys_sample,omitempty"`
	HasFeatureFlags   bool     `json:"has_feature_flags"`
	FeatureFlagsBytes int      `json:"feature_flags_bytes"`
}

func (c *Client) GetBrowserCredentialsDebugStatus() BrowserCredentialsDebugStatus {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()

	// Base status
	base := BrowserCredentialsStatus{
		HasCredentials: credsStore.creds != nil,
		IsExpired:      true,
	}
	if credsStore.creds != nil {
		base.UpdatedAt = &credsStore.creds.UpdatedAt
		base.ExpiresAt = &credsStore.creds.ExpiresAt
		base.IsExpired = time.Now().After(credsStore.creds.ExpiresAt)
	}

	// Debug fields
	queryIDCount := 0
	keys := make([]string, 0, 16)
	if credsStore.creds != nil && credsStore.creds.QueryIDs != nil {
		for k := range credsStore.creds.QueryIDs {
			queryIDCount++
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 12 {
		keys = keys[:12]
	}
	ffBytes := 0
	if credsStore.creds != nil {
		ffBytes = len(credsStore.creds.FeatureFlags)
	}

	return BrowserCredentialsDebugStatus{
		BrowserCredentialsStatus: base,
		HasQueryIDs:             queryIDCount > 0,
		QueryIDCount:            queryIDCount,
		QueryIDKeysSample:       keys,
		HasFeatureFlags:         ffBytes > 0,
		FeatureFlagsBytes:       ffBytes,
	}
}

// GetBrowserCredentialsStatus returns the current status of browser credentials.
func (c *Client) GetBrowserCredentialsStatus() BrowserCredentialsStatus {
	credsStore.mu.RLock()
	defer credsStore.mu.RUnlock()

	if credsStore.creds == nil {
		return BrowserCredentialsStatus{
			HasCredentials: false,
			IsExpired:      true,
		}
	}

	return BrowserCredentialsStatus{
		HasCredentials: true,
		UpdatedAt:      &credsStore.creds.UpdatedAt,
		ExpiresAt:      &credsStore.creds.ExpiresAt,
		IsExpired:      time.Now().After(credsStore.creds.ExpiresAt),
	}
}
