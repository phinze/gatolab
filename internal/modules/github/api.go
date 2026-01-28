package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// PRStats holds counts of PRs in different states (for authored PRs).
type PRStats struct {
	WaitingForReview int
	Approved         int
	ChangesRequested int
	CIFailed         int
}

// ReviewStats holds the count of PRs awaiting my review.
type ReviewStats struct {
	Total int
}

// PRStatus represents the review status of a PR.
type PRStatus string

const (
	PRStatusWaiting  PRStatus = "waiting"
	PRStatusApproved PRStatus = "approved"
	PRStatusChanges  PRStatus = "changes"
)

// CIStatus represents the CI check status of a PR.
type CIStatus string

const (
	CIStatusPending CIStatus = "pending"
	CIStatusPassed  CIStatus = "passed"
	CIStatusFailed  CIStatus = "failed"
)

// PRInfo holds information about a single PR.
type PRInfo struct {
	Title    string
	Repo     string
	Number   int
	Status   PRStatus
	CI       CIStatus
	URL      string
	HeadSHA  string // For fetching CI status
}

// Client is a GitHub API client.
type Client struct {
	token      string
	httpClient *http.Client
	username   string // cached username
}

// NewClient creates a new GitHub API client using the gh CLI token.
func NewClient() (*Client, error) {
	// Get token from gh CLI
	cmd := exec.Command("gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get gh auth token: %w", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return nil, fmt.Errorf("gh auth token is empty")
	}

	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

// GetMyPRStats fetches stats about the authenticated user's PRs.
func (c *Client) GetMyPRStats(ctx context.Context) (PRStats, error) {
	var stats PRStats

	// Get username first
	username, err := c.getAuthenticatedUser(ctx)
	if err != nil {
		return stats, fmt.Errorf("failed to get username: %w", err)
	}

	// Fetch counts in parallel
	// We get total, approved, and changes_requested, then calculate waiting
	type result struct {
		field string
		count int
		err   error
	}
	results := make(chan result, 3)

	queries := []struct {
		field string
		query string
	}{
		{"total", fmt.Sprintf("is:pr author:%s is:open", username)},
		{"approved", fmt.Sprintf("is:pr author:%s is:open review:approved", username)},
		{"changes", fmt.Sprintf("is:pr author:%s is:open review:changes_requested", username)},
	}

	for _, q := range queries {
		go func(field, query string) {
			count, err := c.searchPRCount(ctx, query)
			results <- result{field, count, err}
		}(q.field, q.query)
	}

	var total int
	for range 3 {
		r := <-results
		if r.err != nil {
			return stats, r.err
		}
		switch r.field {
		case "total":
			total = r.count
		case "approved":
			stats.Approved = r.count
		case "changes":
			stats.ChangesRequested = r.count
		}
	}

	// Waiting = total - approved - changes_requested
	stats.WaitingForReview = total - stats.Approved - stats.ChangesRequested

	return stats, nil
}

// getAuthenticatedUser returns the authenticated user's login (cached after first call).
func (c *Client) getAuthenticatedUser(ctx context.Context) (string, error) {
	// Return cached username if available
	if c.username != "" {
		return c.username, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error: %s", resp.Status)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}

	// Cache the username
	c.username = user.Login
	return c.username, nil
}

// searchPRCount searches for PRs matching a query and returns the count.
func (c *Client) searchPRCount(ctx context.Context, query string) (int, error) {
	apiURL := "https://api.github.com/search/issues?per_page=1&q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API error: %s", resp.Status)
	}

	var result struct {
		TotalCount int `json:"total_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return result.TotalCount, nil
}

// GetMyPRList fetches a list of PRs with details including CI status.
func (c *Client) GetMyPRList(ctx context.Context) ([]PRInfo, error) {
	username, err := c.getAuthenticatedUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Fetch all open PRs, approved PRs, and changes requested PRs in parallel
	type result struct {
		category string
		prs      []PRInfo
		err      error
	}
	results := make(chan result, 3)

	queries := []struct {
		category string
		query    string
	}{
		{"all", fmt.Sprintf("is:pr author:%s is:open", username)},
		{"approved", fmt.Sprintf("is:pr author:%s is:open review:approved", username)},
		{"changes", fmt.Sprintf("is:pr author:%s is:open review:changes_requested", username)},
	}

	for _, q := range queries {
		go func(category, query string) {
			prs, err := c.searchPRs(ctx, query, PRStatusWaiting) // Status will be set later
			results <- result{category, prs, err}
		}(q.category, q.query)
	}

	var allPRs, approvedPRs, changesPRs []PRInfo
	for range 3 {
		r := <-results
		if r.err != nil {
			return nil, r.err
		}
		switch r.category {
		case "all":
			allPRs = r.prs
		case "approved":
			approvedPRs = r.prs
		case "changes":
			changesPRs = r.prs
		}
	}

	// Build sets of approved and changes-requested PR URLs for quick lookup
	approvedSet := make(map[string]bool)
	for _, pr := range approvedPRs {
		approvedSet[pr.URL] = true
	}
	changesSet := make(map[string]bool)
	for _, pr := range changesPRs {
		changesSet[pr.URL] = true
	}

	// Set correct status for each PR
	for i := range allPRs {
		if approvedSet[allPRs[i].URL] {
			allPRs[i].Status = PRStatusApproved
		} else if changesSet[allPRs[i].URL] {
			allPRs[i].Status = PRStatusChanges
		} else {
			allPRs[i].Status = PRStatusWaiting
		}
	}

	// Fetch CI status for all PRs in parallel
	c.fetchCIStatuses(ctx, allPRs)

	return allPRs, nil
}

// fetchCIStatuses fetches CI status for a list of PRs in parallel.
func (c *Client) fetchCIStatuses(ctx context.Context, prs []PRInfo) {
	if len(prs) == 0 {
		return
	}

	type ciResult struct {
		index int
		ci    CIStatus
	}
	results := make(chan ciResult, len(prs))

	for i, pr := range prs {
		go func(idx int, pr PRInfo) {
			ci := c.getCIStatus(ctx, pr.Repo, pr.HeadSHA)
			results <- ciResult{idx, ci}
		}(i, pr)
	}

	for range len(prs) {
		r := <-results
		prs[r.index].CI = r.ci
	}
}

// getCIStatus fetches the combined CI status for a commit.
func (c *Client) getCIStatus(ctx context.Context, repo, sha string) CIStatus {
	if sha == "" {
		return CIStatusPending
	}

	// Use the combined status endpoint
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s/status", repo, sha)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return CIStatusPending
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CIStatusPending
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CIStatusPending
	}

	var status struct {
		State string `json:"state"` // success, failure, pending, error
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return CIStatusPending
	}

	switch status.State {
	case "success":
		return CIStatusPassed
	case "failure", "error":
		return CIStatusFailed
	default:
		return CIStatusPending
	}
}

// searchPRs searches for PRs matching a query and returns details including head SHA.
func (c *Client) searchPRs(ctx context.Context, query string, status PRStatus) ([]PRInfo, error) {
	apiURL := "https://api.github.com/search/issues?per_page=10&q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}

	var searchResult struct {
		Items []struct {
			Title         string `json:"title"`
			Number        int    `json:"number"`
			HTMLURL       string `json:"html_url"`
			RepositoryURL string `json:"repository_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return nil, err
	}

	var prs []PRInfo
	for _, item := range searchResult.Items {
		// Extract repo name from repository URL
		// https://api.github.com/repos/owner/repo -> owner/repo
		repoName := item.RepositoryURL
		if idx := strings.Index(repoName, "/repos/"); idx != -1 {
			repoName = repoName[idx+7:]
		}

		prs = append(prs, PRInfo{
			Title:  item.Title,
			Repo:   repoName,
			Number: item.Number,
			Status: status,
			URL:    item.HTMLURL,
		})
	}

	// Fetch head SHAs for all PRs in parallel
	c.fetchHeadSHAs(ctx, prs)

	return prs, nil
}

// fetchHeadSHAs fetches the head SHA for each PR in parallel.
func (c *Client) fetchHeadSHAs(ctx context.Context, prs []PRInfo) {
	if len(prs) == 0 {
		return
	}

	type shaResult struct {
		index int
		sha   string
	}
	results := make(chan shaResult, len(prs))

	for i, pr := range prs {
		go func(idx int, pr PRInfo) {
			sha := c.getPRHeadSHA(ctx, pr.Repo, pr.Number)
			results <- shaResult{idx, sha}
		}(i, pr)
	}

	for range len(prs) {
		r := <-results
		prs[r.index].HeadSHA = r.sha
	}
}

// getPRHeadSHA fetches the head SHA for a specific PR.
func (c *Client) getPRHeadSHA(ctx context.Context, repo string, number int) string {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return ""
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return ""
	}

	return pr.Head.SHA
}

// GetReviewRequestedStats fetches the count of PRs awaiting my review.
func (c *Client) GetReviewRequestedStats(ctx context.Context) (ReviewStats, error) {
	var stats ReviewStats

	username, err := c.getAuthenticatedUser(ctx)
	if err != nil {
		return stats, fmt.Errorf("failed to get username: %w", err)
	}

	// Query: is:open is:pr review-requested:{user} archived:false
	query := fmt.Sprintf("is:open is:pr review-requested:%s archived:false", username)
	count, err := c.searchPRCount(ctx, query)
	if err != nil {
		return stats, err
	}

	stats.Total = count
	return stats, nil
}

// GetReviewRequestedPRList fetches PRs awaiting my review with details.
func (c *Client) GetReviewRequestedPRList(ctx context.Context) ([]PRInfo, error) {
	username, err := c.getAuthenticatedUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Query: is:open is:pr review-requested:{user} archived:false
	query := fmt.Sprintf("is:open is:pr review-requested:%s archived:false", username)
	prs, err := c.searchPRs(ctx, query, PRStatusWaiting)
	if err != nil {
		return nil, err
	}

	// For review-requested PRs, the status is always "waiting" (for my review)
	// Fetch CI statuses
	c.fetchCIStatuses(ctx, prs)

	return prs, nil
}
