package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PRStats holds counts of PRs in different states (for authored PRs).
type PRStats struct {
	WaitingForReview int
	Approved         int
	ChangesRequested int
	CIFailed         int
	Draft            int
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
	Title   string
	Repo    string
	Number  int
	Status  PRStatus
	CI      CIStatus
	URL     string
	HeadSHA string // For fetching CI status
	IsDraft bool
}

// Client is a GitHub API client.
type Client struct {
	token      string
	httpClient *http.Client
	username   string // cached username

	// sem admits one request at a time; see do.
	sem chan struct{}
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
		sem: make(chan struct{}, 1),
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

// do sends a request with the standard headers, serialized against every other
// request from this client.
//
// GitHub's secondary rate limit is about concurrency rather than volume: the
// docs ask callers to "make requests for a single user or client ID serially,"
// and this client used to do the opposite. A refresh fans out three searches
// for PR stats, three more for the list, then one request per PR for CI status
// and another per PR for details, all on their own goroutines. At eight open
// PRs that is roughly two dozen requests landing at once, which GitHub started
// refusing outright with a 403 once the PR count grew past the threshold.
//
// Admitting one request at a time costs a couple of seconds per refresh, which
// is nothing against a two minute cadence, and keeps us inside the documented
// contract no matter how many PRs are open.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	select {
	case c.sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	defer func() { <-c.sem }()

	return c.httpClient.Do(req)
}

// apiError builds an error from a non-200 response.
//
// GitHub explains every refusal in the response body, and for throttling it
// also names the exhausted budget in the rate limit headers. Reporting only
// the status line, as this used to, turns a 403 into a guessing game: the
// status alone cannot distinguish a scope problem from a primary rate limit
// from a secondary one, and those want completely different fixes.
func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	detail := strings.TrimSpace(string(body))
	var payload struct {
		Message          string `json:"message"`
		DocumentationURL string `json:"documentation_url"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Message != "" {
		detail = payload.Message
		if payload.DocumentationURL != "" {
			detail += " (" + payload.DocumentationURL + ")"
		}
	}

	msg := "API error: " + resp.Status
	if detail != "" {
		msg += ": " + detail
	}

	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		resets := "an unknown time"
		if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			resets = "in " + time.Until(time.Unix(sec, 0)).Round(time.Second).String()
		}
		msg += fmt.Sprintf(" [%s budget exhausted (limit %s), resets %s]",
			resp.Header.Get("X-RateLimit-Resource"),
			resp.Header.Get("X-RateLimit-Limit"),
			resets)
	} else if retry := resp.Header.Get("Retry-After"); retry != "" {
		msg += " [secondary rate limit, retry after " + retry + "s]"
	}

	return errors.New(msg)
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

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp)
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

	resp, err := c.do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, apiError(resp)
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

	// Sort by repo name, then by PR number within each repo
	sortPRsByRepo(allPRs)

	return allPRs, nil
}

// sortPRsByRepo sorts PRs by repo name alphabetically, then by PR number.
func sortPRsByRepo(prs []PRInfo) {
	sort.Slice(prs, func(i, j int) bool {
		// Compare repo names (just the repo part after /)
		repoI := prs[i].Repo
		if idx := strings.LastIndex(repoI, "/"); idx != -1 {
			repoI = repoI[idx+1:]
		}
		repoJ := prs[j].Repo
		if idx := strings.LastIndex(repoJ, "/"); idx != -1 {
			repoJ = repoJ[idx+1:]
		}

		if repoI != repoJ {
			return repoI < repoJ
		}
		// Same repo, sort by PR number
		return prs[i].Number < prs[j].Number
	})
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

	resp, err := c.do(req)
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

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
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

	// Fetch head SHAs and draft status for all PRs in parallel
	c.fetchPRDetails(ctx, prs)

	return prs, nil
}

// fetchPRDetails fetches the head SHA and draft status for each PR in parallel.
func (c *Client) fetchPRDetails(ctx context.Context, prs []PRInfo) {
	if len(prs) == 0 {
		return
	}

	type detailsResult struct {
		index   int
		details prDetails
	}
	results := make(chan detailsResult, len(prs))

	for i, pr := range prs {
		go func(idx int, pr PRInfo) {
			details := c.getPRDetails(ctx, pr.Repo, pr.Number)
			results <- detailsResult{idx, details}
		}(i, pr)
	}

	for range len(prs) {
		r := <-results
		prs[r.index].HeadSHA = r.details.HeadSHA
		prs[r.index].IsDraft = r.details.IsDraft
	}
}

// prDetails holds extra details fetched from the PR API.
type prDetails struct {
	HeadSHA string
	IsDraft bool
}

// getPRDetails fetches the head SHA and draft status for a specific PR.
func (c *Client) getPRDetails(ctx context.Context, repo string, number int) prDetails {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return prDetails{}
	}

	resp, err := c.do(req)
	if err != nil {
		return prDetails{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return prDetails{}
	}

	var pr struct {
		Draft bool `json:"draft"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return prDetails{}
	}

	return prDetails{
		HeadSHA: pr.Head.SHA,
		IsDraft: pr.Draft,
	}
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
