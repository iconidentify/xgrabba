package twitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOAuth2RefreshTokenSource_Refreshes(t *testing.T) {
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected grant_type: %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("client_id") != "cid" {
			t.Fatalf("unexpected client_id: %q", r.Form.Get("client_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			// first refresh uses initial refresh token
			if r.Form.Get("refresh_token") != "rt1" {
				t.Fatalf("unexpected refresh_token on call1: %q", r.Form.Get("refresh_token"))
			}
			_, _ = w.Write([]byte(`{"access_token":"at1","refresh_token":"rt2","expires_in":3600,"token_type":"bearer"}`))
			return
		}
		// second refresh should use rotated refresh token
		if r.Form.Get("refresh_token") != "rt2" {
			t.Fatalf("unexpected refresh_token on call2: %q", r.Form.Get("refresh_token"))
		}
		_, _ = w.Write([]byte(`{"access_token":"at2","refresh_token":"rt3","expires_in":3600,"token_type":"bearer"}`))
	}))
	defer srv.Close()

	ts := NewOAuth2RefreshTokenSource(OAuth2RefreshTokenSourceConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		RefreshToken: "rt1",
		HTTPTimeout:  2 * time.Second,
	})

	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "at1" {
		t.Fatalf("expected at1, got %q", got)
	}
	// ensure refresh token rotated internally by forcing refresh again (server would accept old but we just check no error)
	if err := ts.ForceRefresh(context.Background()); err != nil {
		t.Fatalf("unexpected refresh error: %v", err)
	}
}

