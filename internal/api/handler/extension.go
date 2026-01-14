package handler

import (
	"encoding/json"
	"net/http"

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

	// Store credentials
	h.twitterClient.SetBrowserCredentials(twitter.BrowserCredentials{
		AuthToken:    req.AuthToken,
		CT0:          req.CT0,
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
	HasCredentials bool    `json:"has_credentials"`
	UpdatedAt      *string `json:"updated_at,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
	IsExpired      bool    `json:"is_expired"`
}

// CredentialsStatus handles GET /api/v1/extension/credentials/status
// Returns the current status of browser credentials.
func (h *ExtensionHandler) CredentialsStatus(w http.ResponseWriter, r *http.Request) {
	status := h.twitterClient.GetBrowserCredentialsStatus()

	resp := CredentialsStatusResponse{
		HasCredentials: status.HasCredentials,
		IsExpired:      status.IsExpired,
	}

	if status.UpdatedAt != nil {
		ts := status.UpdatedAt.Format("2006-01-02T15:04:05Z")
		resp.UpdatedAt = &ts
	}
	if status.ExpiresAt != nil {
		ts := status.ExpiresAt.Format("2006-01-02T15:04:05Z")
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
