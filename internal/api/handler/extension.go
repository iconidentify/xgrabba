package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/iconidentify/xgrabba/pkg/twitter"
)

// ExtensionHandler handles browser extension credential sync endpoints.
type ExtensionHandler struct {
	twitterClient *twitter.Client
}

// NewExtensionHandler creates a new extension handler.
func NewExtensionHandler(twitterClient *twitter.Client) *ExtensionHandler {
	return &ExtensionHandler{
		twitterClient: twitterClient,
	}
}

// SyncCredentialsRequest is the request body for syncing browser credentials.
type SyncCredentialsRequest struct {
	AuthToken    string            `json:"auth_token"`
	CT0          string            `json:"ct0"`
	Cookies      string            `json:"cookies,omitempty"`      // Full cookie string for NSFW/age-restricted content
	QueryIDs     map[string]string `json:"query_ids,omitempty"`
	FeatureFlags json.RawMessage   `json:"feature_flags,omitempty"`
}

// SyncCredentialsResponse is the response for credential sync.
type SyncCredentialsResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// SyncCredentials handles POST /api/v1/extension/credentials
// Receives browser credentials from the extension for server-side GraphQL access.
func (h *ExtensionHandler) SyncCredentials(w http.ResponseWriter, r *http.Request) {
	var req SyncCredentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid request body",
		})
		return
	}

	// Validate required fields
	if req.AuthToken == "" || req.CT0 == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "auth_token and ct0 are required",
		})
		return
	}

	// Debug logging (no secrets):
	// show whether the extension is sending query_ids / feature_flags / cookies.
	queryIDCount := len(req.QueryIDs)
	keys := make([]string, 0, 8)
	for k := range req.QueryIDs {
		if len(keys) < 8 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	ffBytes := len(req.FeatureFlags)
	cookieLen := len(req.Cookies)

	slog.Default().Info("extension credentials received",
		"remote_addr", r.RemoteAddr,
		"has_query_ids", queryIDCount > 0,
		"query_id_count", queryIDCount,
		"query_id_keys_sample", keys,
		"has_feature_flags", ffBytes > 0,
		"feature_flags_bytes", ffBytes,
		"has_full_cookies", cookieLen > 0,
		"cookie_string_len", cookieLen,
	)

	// Store credentials
	h.twitterClient.SetBrowserCredentials(twitter.BrowserCredentials{
		AuthToken:    req.AuthToken,
		CT0:          req.CT0,
		Cookies:      req.Cookies,
		QueryIDs:     req.QueryIDs,
		FeatureFlags: req.FeatureFlags,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SyncCredentialsResponse{
		Status:  "ok",
		Message: "credentials synced successfully",
	})
}

// CredentialsStatusResponse is the response for credential status.
type CredentialsStatusResponse struct {
	HasCredentials    bool     `json:"has_credentials"`
	UpdatedAt         *string  `json:"updated_at,omitempty"`
	ExpiresAt         *string  `json:"expires_at,omitempty"`
	IsExpired         bool     `json:"is_expired"`
	HasQueryIDs       bool     `json:"has_query_ids,omitempty"`
	QueryIDCount      int      `json:"query_id_count,omitempty"`
	QueryIDKeys       []string `json:"query_id_keys_sample,omitempty"`
	HasFeatureFlags   bool     `json:"has_feature_flags,omitempty"`
	FeatureFlagsBytes int      `json:"feature_flags_bytes,omitempty"`
	HasFullCookies    bool     `json:"has_full_cookies,omitempty"`
	CookieStringLen   int      `json:"cookie_string_len,omitempty"`
}

// CredentialsStatus handles GET /api/v1/extension/credentials/status
// Returns the current status of browser credentials.
func (h *ExtensionHandler) CredentialsStatus(w http.ResponseWriter, r *http.Request) {
	debug := h.twitterClient.GetBrowserCredentialsDebugStatus()

	resp := CredentialsStatusResponse{
		HasCredentials:    debug.HasCredentials,
		IsExpired:         debug.IsExpired,
		HasQueryIDs:       debug.HasQueryIDs,
		QueryIDCount:      debug.QueryIDCount,
		QueryIDKeys:       debug.QueryIDKeysSample,
		HasFeatureFlags:   debug.HasFeatureFlags,
		FeatureFlagsBytes: debug.FeatureFlagsBytes,
		HasFullCookies:    debug.HasFullCookies,
		CookieStringLen:   debug.CookieStringLen,
	}

	if debug.UpdatedAt != nil {
		ts := debug.UpdatedAt.Format("2006-01-02T15:04:05Z")
		resp.UpdatedAt = &ts
	}
	if debug.ExpiresAt != nil {
		ts := debug.ExpiresAt.Format("2006-01-02T15:04:05Z")
		resp.ExpiresAt = &ts
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// ClearCredentials handles POST /api/v1/extension/credentials/clear
// Clears stored browser credentials.
func (h *ExtensionHandler) ClearCredentials(w http.ResponseWriter, r *http.Request) {
	h.twitterClient.ClearBrowserCredentials()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// TestUserLookup handles GET /api/v1/extension/test-user-lookup?user_id=...
// Tests the UserByRestId endpoint with current browser credentials.
// This is a debug endpoint to verify the NSFW user lookup fix works.
func (h *ExtensionHandler) TestUserLookup(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = "1685114627024121856" // Default: @psyop4921's user ID from the test case
	}

	slog.Default().Info("testing user lookup", "user_id", userID)

	result := h.twitterClient.TestUserLookup(r.Context(), userID)

	w.Header().Set("Content-Type", "application/json")
	if result.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(result)
}
