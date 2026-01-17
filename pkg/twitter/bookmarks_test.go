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
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "testtoken"},
		Timeout:   2 * time.Second,
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
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
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

func TestBookmarksClient_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	ids, next, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty ids, got %d", len(ids))
	}
	if next != "" {
		t.Errorf("expected empty next token, got %q", next)
	}
}

func TestBookmarksClient_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	_, _, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBookmarksClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	_, _, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestBookmarksClient_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.ListBookmarks(ctx, "1", 100, "")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestBookmarksClient_Pagination(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			w.Write([]byte(`{"data":[{"id":"1"}],"meta":{"next_token":"token2"}}`))
		} else {
			w.Write([]byte(`{"data":[{"id":"2"}]}`))
		}
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	// First page
	ids1, next1, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err != nil {
		t.Fatalf("first page failed: %v", err)
	}
	if len(ids1) != 1 || ids1[0] != "1" {
		t.Errorf("first page ids = %v, want [1]", ids1)
	}
	if next1 != "token2" {
		t.Errorf("next token = %q, want token2", next1)
	}

	// Second page
	ids2, next2, err := c.ListBookmarks(context.Background(), "1", 100, next1)
	if err != nil {
		t.Fatalf("second page failed: %v", err)
	}
	if len(ids2) != 1 || ids2[0] != "2" {
		t.Errorf("second page ids = %v, want [2]", ids2)
	}
	if next2 != "" {
		t.Errorf("next token = %q, want empty", next2)
	}
}

func TestBookmarksClient_NetworkError(t *testing.T) {
	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   "http://invalid-domain-that-does-not-exist-12345.com",
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   1 * time.Second,
	})

	_, _, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestBookmarksClient_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"message":"Unauthorized"}]}`))
	}))
	defer srv.Close()

	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    &StaticTokenSource{TokenValue: "t"},
		Timeout:   2 * time.Second,
	})

	_, _, err := c.ListBookmarks(context.Background(), "1", 100, "")
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
}

