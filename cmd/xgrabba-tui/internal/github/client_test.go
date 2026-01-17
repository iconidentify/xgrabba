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

func TestNewClient(t *testing.T) {
	client := NewClient("token", "owner", "repo", "")
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if !client.Enabled() {
		t.Error("client should be enabled with owner and repo")
	}
}

func TestNewClient_EmptyOwner(t *testing.T) {
	client := NewClient("token", "", "repo", "")
	if client.Enabled() {
		t.Error("client should not be enabled without owner")
	}
}

func TestNewClient_EmptyRepo(t *testing.T) {
	client := NewClient("token", "owner", "", "")
	if client.Enabled() {
		t.Error("client should not be enabled without repo")
	}
}

func TestNewClient_CustomBaseURL(t *testing.T) {
	client := NewClient("token", "owner", "repo", "https://custom.github.com")
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestGetRepo_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := map[string]any{
			"full_name":      "owner/repo",
			"description":    "Test repo",
			"default_branch": "main",
			"open_issues_count": 5,
			"stargazers_count":  10,
			"forks_count":       3,
			"updated_at":        time.Now().Format(time.RFC3339),
			"html_url":         "https://github.com/owner/repo",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	repo, err := client.GetRepo(context.Background())
	if err != nil {
		t.Fatalf("GetRepo failed: %v", err)
	}
	if repo.FullName != "owner/repo" {
		t.Errorf("FullName = %q, want owner/repo", repo.FullName)
	}
	if repo.Description != "Test repo" {
		t.Errorf("Description = %q, want Test repo", repo.Description)
	}
}

func TestGetRepo_NetworkError(t *testing.T) {
	client := NewClient("token", "owner", "repo", "http://invalid-domain-that-does-not-exist-12345.com")
	_, err := client.GetRepo(context.Background())
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestGetRepo_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	_, err := client.GetRepo(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestListWorkflowRuns_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/owner/repo/actions/runs") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := map[string]any{
			"workflow_runs": []map[string]any{
				{
					"id":          int64(123),
					"name":        "CI",
					"event":       "push",
					"status":      "completed",
					"conclusion":   "success",
					"head_branch": "main",
					"actor":       map[string]string{"login": "user"},
					"created_at":  time.Now().Format(time.RFC3339),
					"updated_at":  time.Now().Format(time.RFC3339),
					"html_url":    "https://github.com/owner/repo/actions/runs/123",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	runs, err := client.ListWorkflowRuns(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListWorkflowRuns failed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Name != "CI" {
		t.Errorf("Name = %q, want CI", runs[0].Name)
	}
}

func TestListWorkflowRuns_NetworkError(t *testing.T) {
	client := NewClient("token", "owner", "repo", "http://invalid-domain-that-does-not-exist-12345.com")
	_, err := client.ListWorkflowRuns(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestListReleases_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/owner/repo/releases") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := []map[string]any{
			{
				"name":         "v1.0.0",
				"tag_name":   "v1.0.0",
				"draft":       false,
				"prerelease":  false,
				"published_at": time.Now().Format(time.RFC3339),
				"html_url":    "https://github.com/owner/repo/releases/tag/v1.0.0",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	releases, err := client.ListReleases(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListReleases failed: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	if releases[0].TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want v1.0.0", releases[0].TagName)
	}
}

func TestListReleases_NetworkError(t *testing.T) {
	client := NewClient("token", "owner", "repo", "http://invalid-domain-that-does-not-exist-12345.com")
	_, err := client.ListReleases(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestListIssues_AllStates(t *testing.T) {
	states := []string{"open", "closed", "all"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.RawQuery, "state="+state) {
					t.Errorf("expected state=%s in query, got %s", state, r.URL.RawQuery)
				}
				json.NewEncoder(w).Encode([]map[string]any{})
			}))
			defer server.Close()

			client := NewClient("token", "owner", "repo", server.URL)
			_, err := client.ListIssues(context.Background(), state, 10)
			if err != nil {
				t.Fatalf("ListIssues(%s) failed: %v", state, err)
			}
		})
	}
}

func TestListIssues_DefaultState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "state=open") {
			t.Errorf("expected default state=open, got %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	_, err := client.ListIssues(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("ListIssues with empty state failed: %v", err)
	}
}

func TestListIssues_NetworkError(t *testing.T) {
	client := NewClient("token", "owner", "repo", "http://invalid-domain-that-does-not-exist-12345.com")
	_, err := client.ListIssues(context.Background(), "open", 10)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestUpdateIssue_PartialUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload IssueUpdate
		json.NewDecoder(r.Body).Decode(&payload)
		resp := map[string]any{
			"number":     42,
			"title":      payload.Title,
			"state":      "open", // Keep original
			"body":       payload.Body,
			"labels":     []map[string]any{},
			"updated_at": time.Now().Format(time.RFC3339),
			"html_url":   "https://example.com/issues/42",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	issue, err := client.UpdateIssue(context.Background(), 42, IssueUpdate{
		Title: "Updated title",
		// State and Body not set - should be partial update
	})
	if err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}
	if issue.Title != "Updated title" {
		t.Errorf("Title = %q, want Updated title", issue.Title)
	}
}

func TestUpdateIssue_NetworkError(t *testing.T) {
	client := NewClient("token", "owner", "repo", "http://invalid-domain-that-does-not-exist-12345.com")
	_, err := client.UpdateIssue(context.Background(), 42, IssueUpdate{Title: "Test"})
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestUpdateIssue_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	client := NewClient("token", "owner", "repo", server.URL)
	_, err := client.UpdateIssue(context.Background(), 999, IssueUpdate{Title: "Test"})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}
