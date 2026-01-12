package twitter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestBookmarksClient_ListBookmarks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testtoken" {
			t.Fatalf("expected auth header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"111"},{"id":"222"}],"meta":{"next_token":"abc"}}`))
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:     srv.URL,
		BearerToken: "testtoken",
		Timeout:     2 * time.Second,
	})

	ids, next, err := c.ListBookmarks(context.Background(), "123", 100, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "abc" {
		t.Fatalf("expected next_token abc, got %q", next)
	}
	if len(ids) != 2 || ids[0] != "111" || ids[1] != "222" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
}

func TestBookmarksClient_RateLimited(t *testing.T) {
	reset := time.Now().Add(60 * time.Second).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-rate-limit-reset", strconv.FormatInt(reset, 10))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:     srv.URL,
		BearerToken: "t",
		Timeout:     2 * time.Second,
	})
	_, _, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rl.Reset.IsZero() {
		t.Fatalf("expected reset")
	}
}

