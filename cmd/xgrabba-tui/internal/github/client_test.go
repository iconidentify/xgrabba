package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListIssuesFiltersPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/issues") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		payload := []map[string]any{
			{
				"number":     1,
				"title":      "Bug report",
				"state":      "open",
				"body":       "Details",
				"labels":     []map[string]any{{"name": "bug"}},
				"updated_at": time.Now().Format(time.RFC3339),
				"html_url":   "https://example.com/issues/1",
			},
			{
				"number": 2,
				"title":  "PR masquerading",
				"state":  "open",
				"pull_request": map[string]any{
					"url": "https://example.com/pulls/2",
				},
				"updated_at": time.Now().Format(time.RFC3339),
				"html_url":   "https://example.com/pulls/2",
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	client := NewClient("", "acme", "widgets", server.URL)
	issues, err := client.ListIssues(context.Background(), "open", 10)
	if err != nil {
		t.Fatalf("ListIssues error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Number != 1 {
		t.Fatalf("expected issue #1, got #%d", issues[0].Number)
	}
	if len(issues[0].Labels) != 1 || issues[0].Labels[0] != "bug" {
		t.Fatalf("expected label 'bug', got %v", issues[0].Labels)
	}
}

func TestUpdateIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected PATCH, got %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/issues/42" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var payload IssueUpdate
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Title != "New title" || payload.State != "closed" {
			t.Fatalf("unexpected payload: %+v", payload)
		}

		resp := map[string]any{
			"number":     42,
			"title":      payload.Title,
			"state":      payload.State,
			"body":       payload.Body,
			"labels":     []map[string]any{{"name": "triage"}},
			"updated_at": time.Now().Format(time.RFC3339),
			"html_url":   "https://example.com/issues/42",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("token", "acme", "widgets", server.URL)
	issue, err := client.UpdateIssue(context.Background(), 42, IssueUpdate{
		Title:  "New title",
		Body:   "Body",
		State:  "closed",
		Labels: []string{"triage"},
	})
	if err != nil {
		t.Fatalf("UpdateIssue error: %v", err)
	}
	if issue.Number != 42 || issue.State != "closed" {
		t.Fatalf("unexpected issue response: %+v", issue)
	}
}
