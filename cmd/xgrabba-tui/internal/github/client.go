// Package github provides GitHub API integrations for the TUI.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client wraps GitHub API access for a repo.
type Client struct {
	baseURL    string
	owner      string
	repo       string
	token      string
	httpClient *http.Client
}

// NewClient creates a new GitHub API client.
func NewClient(token, owner, repo, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		owner:      owner,
		repo:       repo,
		token:      token,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Enabled indicates if the client has a repo configured.
func (c *Client) Enabled() bool {
	return c.owner != "" && c.repo != ""
}

// RepoInfo holds high-level repository info.
type RepoInfo struct {
	FullName      string
	Description   string
	DefaultBranch string
	OpenIssues    int
	Stars         int
	Forks         int
	UpdatedAt     time.Time
	HTMLURL       string
}

// WorkflowRun represents a GitHub Actions workflow run.
type WorkflowRun struct {
	ID         int64
	Name       string
	Event      string
	Status     string
	Conclusion string
	Branch     string
	Actor      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	HTMLURL    string
}

// Release represents a GitHub release.
type Release struct {
	Name        string
	TagName     string
	Draft       bool
	Prerelease  bool
	PublishedAt time.Time
	HTMLURL     string
}

// Issue represents a GitHub issue.
type Issue struct {
	Number    int
	Title     string
	State     string
	Body      string
	Labels    []string
	Assignee  string
	UpdatedAt time.Time
	HTMLURL   string
}

// IssueUpdate contains fields for updating issues.
type IssueUpdate struct {
	Title  string   `json:"title,omitempty"`
	Body   string   `json:"body,omitempty"`
	State  string   `json:"state,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// GetRepo returns repository metadata.
func (c *Client) GetRepo(ctx context.Context) (*RepoInfo, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s", c.baseURL, c.owner, c.repo)
	body, _, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var payload struct {
		FullName      string    `json:"full_name"`
		Description   string    `json:"description"`
		DefaultBranch string    `json:"default_branch"`
		OpenIssues    int       `json:"open_issues_count"`
		Stars         int       `json:"stargazers_count"`
		Forks         int       `json:"forks_count"`
		UpdatedAt     time.Time `json:"updated_at"`
		HTMLURL       string    `json:"html_url"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse repo info: %w", err)
	}

	return &RepoInfo{
		FullName:      payload.FullName,
		Description:   payload.Description,
		DefaultBranch: payload.DefaultBranch,
		OpenIssues:    payload.OpenIssues,
		Stars:         payload.Stars,
		Forks:         payload.Forks,
		UpdatedAt:     payload.UpdatedAt,
		HTMLURL:       payload.HTMLURL,
	}, nil
}

// ListWorkflowRuns returns recent workflow runs.
func (c *Client) ListWorkflowRuns(ctx context.Context, limit int) ([]WorkflowRun, error) {
	if limit <= 0 {
		limit = 10
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/runs", c.baseURL, c.owner, c.repo)
	endpoint = addQuery(endpoint, map[string]string{"per_page": fmt.Sprintf("%d", limit)})
	body, _, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var payload struct {
		WorkflowRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Event      string `json:"event"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Branch     string `json:"head_branch"`
			Actor      struct {
				Login string `json:"login"`
			} `json:"actor"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			HTMLURL   string    `json:"html_url"`
		} `json:"workflow_runs"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse workflow runs: %w", err)
	}

	var runs []WorkflowRun
	for _, run := range payload.WorkflowRuns {
		runs = append(runs, WorkflowRun{
			ID:         run.ID,
			Name:       run.Name,
			Event:      run.Event,
			Status:     run.Status,
			Conclusion: run.Conclusion,
			Branch:     run.Branch,
			Actor:      run.Actor.Login,
			CreatedAt:  run.CreatedAt,
			UpdatedAt:  run.UpdatedAt,
			HTMLURL:    run.HTMLURL,
		})
	}
	return runs, nil
}

// ListReleases returns recent releases.
func (c *Client) ListReleases(ctx context.Context, limit int) ([]Release, error) {
	if limit <= 0 {
		limit = 10
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases", c.baseURL, c.owner, c.repo)
	endpoint = addQuery(endpoint, map[string]string{"per_page": fmt.Sprintf("%d", limit)})
	body, _, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var payload []struct {
		Name        string    `json:"name"`
		TagName     string    `json:"tag_name"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
		HTMLURL     string    `json:"html_url"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	releases := make([]Release, 0, len(payload))
	for _, item := range payload {
		releases = append(releases, Release{
			Name:        item.Name,
			TagName:     item.TagName,
			Draft:       item.Draft,
			Prerelease:  item.Prerelease,
			PublishedAt: item.PublishedAt,
			HTMLURL:     item.HTMLURL,
		})
	}
	return releases, nil
}

// ListIssues returns recent issues (excludes pull requests).
func (c *Client) ListIssues(ctx context.Context, state string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 20
	}
	if state == "" {
		state = "open"
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", c.baseURL, c.owner, c.repo)
	endpoint = addQuery(endpoint, map[string]string{
		"state":    state,
		"per_page": fmt.Sprintf("%d", limit),
	})

	body, _, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var payload []struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		State       string    `json:"state"`
		Body        string    `json:"body"`
		UpdatedAt   time.Time `json:"updated_at"`
		HTMLURL     string    `json:"html_url"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignee *struct {
			Login string `json:"login"`
		} `json:"assignee"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}

	var issues []Issue
	for _, item := range payload {
		if item.PullRequest != nil {
			continue
		}
		labels := make([]string, 0, len(item.Labels))
		for _, label := range item.Labels {
			labels = append(labels, label.Name)
		}

		assignee := ""
		if item.Assignee != nil {
			assignee = item.Assignee.Login
		}

		issues = append(issues, Issue{
			Number:    item.Number,
			Title:     item.Title,
			State:     item.State,
			Body:      item.Body,
			Labels:    labels,
			Assignee:  assignee,
			UpdatedAt: item.UpdatedAt,
			HTMLURL:   item.HTMLURL,
		})
	}
	return issues, nil
}

// UpdateIssue updates an issue.
func (c *Client) UpdateIssue(ctx context.Context, number int, update IssueUpdate) (*Issue, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", c.baseURL, c.owner, c.repo, number)
	body, _, err := c.doRequest(ctx, http.MethodPatch, endpoint, update)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		Body      string    `json:"body"`
		UpdatedAt time.Time `json:"updated_at"`
		HTMLURL   string    `json:"html_url"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Assignee *struct {
			Login string `json:"login"`
		} `json:"assignee"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse updated issue: %w", err)
	}

	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		labels = append(labels, label.Name)
	}

	assignee := ""
	if payload.Assignee != nil {
		assignee = payload.Assignee.Login
	}

	return &Issue{
		Number:    payload.Number,
		Title:     payload.Title,
		State:     payload.State,
		Body:      payload.Body,
		Labels:    labels,
		Assignee:  assignee,
		UpdatedAt: payload.UpdatedAt,
		HTMLURL:   payload.HTMLURL,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, endpoint string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(payload); err != nil {
			return nil, 0, fmt.Errorf("encode payload: %w", err)
		}
		body = buf
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "xgrabba-tui")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("github api (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, resp.StatusCode, nil
}

func addQuery(endpoint string, values map[string]string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := u.Query()
	for key, value := range values {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
