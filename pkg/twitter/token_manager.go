package twitter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource provides bearer tokens for API calls.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context) error
}

// StaticTokenSource uses a fixed bearer token (no refresh).
type StaticTokenSource struct {
	TokenValue string
}

func (s *StaticTokenSource) Token(ctx context.Context) (string, error) {
	if strings.TrimSpace(s.TokenValue) == "" {
		return "", fmt.Errorf("token is empty")
	}
	return s.TokenValue, nil
}

func (s *StaticTokenSource) ForceRefresh(ctx context.Context) error { return nil }

type OAuth2RefreshTokenSourceConfig struct {
	// TokenURL is the OAuth2 token endpoint (e.g. https://api.x.com/2/oauth2/token).
	TokenURL string
	ClientID string
	// ClientSecret optional; when set we use HTTP Basic auth.
	ClientSecret string
	RefreshToken  string

	HTTPTimeout time.Duration
	UserAgent   string
	// RefreshSkew is subtracted from expires_in when scheduling refresh.
	RefreshSkew time.Duration
}

// OAuth2RefreshTokenSource refreshes access tokens via refresh_token grant.
// This assumes you obtained a refresh token out-of-band (internal flow / portal tooling).
type OAuth2RefreshTokenSource struct {
	cfg OAuth2RefreshTokenSourceConfig
	hc  *http.Client

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
}

func NewOAuth2RefreshTokenSource(cfg OAuth2RefreshTokenSourceConfig) *OAuth2RefreshTokenSource {
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://api.x.com/2/oauth2/token"
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 15 * time.Second
	}
	if cfg.RefreshSkew == 0 {
		cfg.RefreshSkew = 30 * time.Second
	}
	return &OAuth2RefreshTokenSource{
		cfg: cfg,
		hc: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
		refreshToken: cfg.RefreshToken,
	}
}

func (s *OAuth2RefreshTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	token := s.accessToken
	exp := s.expiresAt
	s.mu.Unlock()

	if token == "" || ( !exp.IsZero() && time.Until(exp) <= s.cfg.RefreshSkew) {
		if err := s.refresh(ctx); err != nil {
			return "", err
		}
		s.mu.Lock()
		token = s.accessToken
		s.mu.Unlock()
	}
	if token == "" {
		return "", fmt.Errorf("no access token available")
	}
	return token, nil
}

func (s *OAuth2RefreshTokenSource) ForceRefresh(ctx context.Context) error {
	return s.refresh(ctx)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func (s *OAuth2RefreshTokenSource) refresh(ctx context.Context) error {
	s.mu.Lock()
	rt := s.refreshToken
	s.mu.Unlock()

	if strings.TrimSpace(rt) == "" {
		return fmt.Errorf("refresh token is empty")
	}
	if strings.TrimSpace(s.cfg.ClientID) == "" {
		return fmt.Errorf("oauth client_id is empty")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", rt)
	// Some servers require client_id in body even with Basic auth.
	form.Set("client_id", s.cfg.ClientID)

	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if s.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", s.cfg.UserAgent)
	}
	if s.cfg.ClientSecret != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(s.cfg.ClientID + ":" + s.cfg.ClientSecret))
		req.Header.Set("Authorization", "Basic "+basic)
	}

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("token endpoint error: %s", resp.Status)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("token response missing access_token")
	}

	expAt := time.Time{}
	if tr.ExpiresIn > 0 {
		expAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}

	s.mu.Lock()
	s.accessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		s.refreshToken = tr.RefreshToken
	}
	s.expiresAt = expAt
	s.mu.Unlock()

	return nil
}

