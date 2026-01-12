package twitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type flipTokenSource struct {
	tok string
}

func (f *flipTokenSource) Token(ctx context.Context) (string, error) { return f.tok, nil }
func (f *flipTokenSource) ForceRefresh(ctx context.Context) error {
	f.tok = "good"
	return nil
}

func TestBookmarksClient_RetryOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"1"}],"meta":{}}`))
	}))
	defer srv.Close()

	ts := &flipTokenSource{tok: "bad"}
	c := NewBookmarksClient(BookmarksClientConfig{
		BaseURL:   srv.URL,
		Tokens:    ts,
		Timeout:   2 * time.Second,
	})

	ids, _, err := c.ListBookmarks(context.Background(), "u", 10, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
	if calls < 2 {
		t.Fatalf("expected retry, calls=%d", calls)
	}
}

