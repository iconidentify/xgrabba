package bookmarks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOAuthStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "store.json")

	if err := SaveOAuthStore(p, OAuthStore{UserID: "123", RefreshToken: "rt"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := LoadOAuthStore(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if st.UserID != "123" || st.RefreshToken != "rt" {
		t.Fatalf("unexpected store: %#v", st)
	}

	if err := DeleteOAuthStore(p); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("expected deleted, got err=%v", err)
	}
}

