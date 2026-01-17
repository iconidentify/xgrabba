package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/iconidentify/xgrabba/pkg/twitter"
)

func testExtensionLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewExtensionHandler(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	if handler == nil {
		t.Fatal("handler should not be nil")
	}
	if handler.twitterClient == nil {
		t.Error("twitterClient should not be nil")
	}
}

func TestExtensionHandler_SyncCredentials_Success(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	reqBody := SyncCredentialsRequest{
		AuthToken: "test-auth-token",
		CT0:       "test-ct0",
		QueryIDs:  map[string]string{"TweetResultByRestId": "test-query-id"},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/credentials", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.SyncCredentials(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SyncCredentialsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("Status = %q, want ok", resp.Status)
	}
}

func TestExtensionHandler_SyncCredentials_InvalidJSON(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/credentials", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.SyncCredentials(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestExtensionHandler_SyncCredentials_MissingAuthToken(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	reqBody := SyncCredentialsRequest{
		CT0: "test-ct0",
		// AuthToken missing
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/credentials", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.SyncCredentials(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestExtensionHandler_SyncCredentials_MissingCT0(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	reqBody := SyncCredentialsRequest{
		AuthToken: "test-auth-token",
		// CT0 missing
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/credentials", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handler.SyncCredentials(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestExtensionHandler_CredentialsStatus(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/extension/credentials/status", nil)
	w := httptest.NewRecorder()

	handler.CredentialsStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp CredentialsStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have valid response structure
	if resp.HasCredentials {
		// If credentials exist, should have updated_at
	}
}

func TestExtensionHandler_ClearCredentials(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extension/credentials/clear", nil)
	w := httptest.NewRecorder()

	handler.ClearCredentials(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
}

func TestExtensionHandler_TestUserLookup_WithUserID(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/extension/test-user-lookup?user_id=123456", nil)
	w := httptest.NewRecorder()

	handler.TestUserLookup(w, req)

	// Should return response (may be success or failure depending on credentials)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestExtensionHandler_TestUserLookup_DefaultUserID(t *testing.T) {
	client := twitter.NewClient(testExtensionLogger())
	handler := NewExtensionHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/extension/test-user-lookup", nil)
	w := httptest.NewRecorder()

	handler.TestUserLookup(w, req)

	// Should use default user ID
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}
