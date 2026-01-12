package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/bookmarks"
	"github.com/iconidentify/xgrabba/internal/config"
)

type BookmarksOAuthHandler struct {
	cfg    config.BookmarksConfig
	apiKey string
	logger *slog.Logger

	// X API token exchange and user lookup
	tokenURL string

	// in-memory state store for PKCE (single replica)
	mu     sync.Mutex
	states map[string]pkceState
}

type pkceState struct {
	Verifier    string
	RedirectURI string
	UserID      string
	AllowInfer  bool
	CreatedAt   time.Time
}

func NewBookmarksOAuthHandler(cfg config.BookmarksConfig, apiKey string, logger *slog.Logger) *BookmarksOAuthHandler {
	return &BookmarksOAuthHandler{
		cfg:     cfg,
		apiKey:  apiKey,
		logger:  logger,
		tokenURL: cfg.TokenURL,
		states:  make(map[string]pkceState),
	}
}

func (h *BookmarksOAuthHandler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *BookmarksOAuthHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *BookmarksOAuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	store, err := bookmarks.LoadOAuthStore(h.cfg.OAuthStorePath)
	if err != nil {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"connected": false,
			"error":     err.Error(),
		})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"connected":   store.RefreshToken != "" && store.UserID != "",
		"user_id":     store.UserID,
		"updated_at":  store.UpdatedAt,
	})
}

func (h *BookmarksOAuthHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	if err := bookmarks.DeleteOAuthStore(h.cfg.OAuthStorePath); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete oauth store")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *BookmarksOAuthHandler) Start(w http.ResponseWriter, r *http.Request) {
	// Start is intentionally unauthenticated (browser navigation); protect with API key query param.
	if h.apiKey != "" {
		if r.URL.Query().Get("key") != h.apiKey {
			h.writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
	}
	if h.cfg.OAuthClientID == "" {
		h.writeError(w, http.StatusBadRequest, "TWITTER_OAUTH_CLIENT_ID not configured")
		return
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	allowInfer := r.URL.Query().Get("infer_user_id") == "1"
	if userID == "" && !allowInfer {
		h.writeError(w, http.StatusBadRequest, "missing user_id (numeric X user id required) or set infer_user_id=1")
		return
	}

	redirectURI := h.callbackURLFromRequest(r)
	verifier := randomURLSafe(64)
	challenge := pkceChallenge(verifier)
	state := randomURLSafe(24)

	h.mu.Lock()
	h.states[state] = pkceState{Verifier: verifier, RedirectURI: redirectURI, UserID: userID, AllowInfer: allowInfer, CreatedAt: time.Now()}
	h.mu.Unlock()

	authURL := "https://twitter.com/i/oauth2/authorize"
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", h.cfg.OAuthClientID)
	q.Set("redirect_uri", redirectURI)
	// We intentionally do NOT call any X API endpoints here besides the OAuth token endpoint.
	// Scope includes users.read because /2/users/:id/bookmarks requires it on many apps.
	q.Set("scope", "bookmark.read tweet.read users.read offline.access")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	http.Redirect(w, r, authURL+"?"+q.Encode(), http.StatusFound)
}

func (h *BookmarksOAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		h.writeError(w, http.StatusBadRequest, "missing code or state")
		return
	}

	h.mu.Lock()
	ps, ok := h.states[state]
	if ok {
		delete(h.states, state)
	}
	h.mu.Unlock()
	if !ok {
		h.writeError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}
	if time.Since(ps.CreatedAt) > 10*time.Minute {
		h.writeError(w, http.StatusBadRequest, "state expired")
		return
	}

	// Exchange code -> tokens
	tr, err := exchangeAuthCode(r.Context(), h.tokenURL, h.cfg.OAuthClientID, h.cfg.OAuthClientSecret, code, ps.RedirectURI, ps.Verifier)
	if err != nil {
		h.logger.Warn("oauth code exchange failed", "error", err)
		h.writeError(w, http.StatusBadGateway, "token exchange failed")
		return
	}
	if tr.RefreshToken == "" {
		h.writeError(w, http.StatusBadGateway, "missing refresh_token (ensure offline.access scope)")
		return
	}

	userID := ps.UserID
	if userID == "" {
		if !ps.AllowInfer {
			h.writeError(w, http.StatusBadRequest, "missing user id; restart connect flow with user_id or infer_user_id=1")
			return
		}
		// One-time user id lookup. This is ONLY used during connect when user didn't provide a numeric id.
		inferred, err := fetchUserID(r.Context(), "https://api.x.com/2", tr.AccessToken)
		if err != nil {
			h.logger.Warn("fetch users/me failed", "error", err)
			h.writeError(w, http.StatusBadGateway, "failed to infer user id")
			return
		}
		userID = inferred
	}

	if err := bookmarks.SaveOAuthStore(h.cfg.OAuthStorePath, bookmarks.OAuthStore{
		UserID:       userID,
		RefreshToken: tr.RefreshToken,
	}); err != nil {
		h.logger.Warn("failed to save oauth store", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to save token")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<html><body><h3>Connected!</h3><p>You can close this window and return to XGrabba.</p><p>If bookmarks monitoring is enabled, the service will start polling automatically within ~10 seconds.</p></body></html>`))
}

func (h *BookmarksOAuthHandler) callbackURLFromRequest(r *http.Request) string {
	// Respect reverse proxies
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return fmt.Sprintf("%s://%s/bookmarks/oauth/callback", proto, host)
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func exchangeAuthCode(ctx context.Context, tokenURL, clientID, clientSecret, code, redirectURI, verifier string) (*oauthTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if clientSecret != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint status: %s", resp.Status)
	}
	var tr oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

func fetchUserID(ctx context.Context, baseURL, accessToken string) (string, error) {
	u := strings.TrimRight(baseURL, "/") + "/users/me"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("users/me status: %s", resp.Status)
	}
	var parsed struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.Data.ID == "" {
		return "", fmt.Errorf("users/me missing id")
	}
	return parsed.Data.ID, nil
}

