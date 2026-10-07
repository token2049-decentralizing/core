package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

// PR status: GET /api/repos/:owner/:repo/prs/:number/status reads the pull request live from
// GitHub, judged the way the CRE workflow judges it (cre/test-workflow/github.go, scoring.go),
// so the dashboard shows what the next evaluation will see.

// Same as the workflow: "closes #12", "fixes #3", "resolved #7"... in the PR description.
var prLinkedIssueRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)

// prLinkedIssues mirrors the workflow's parseLinkedIssues: unique numbers, ascending.
func prLinkedIssues(body string) []int {
	seen := map[int]bool{}
	ids := []int{}
	for _, m := range prLinkedIssueRe.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if !seen[n] {
			seen[n] = true
			ids = append(ids, n)
		}
	}
	sort.Ints(ids)
	return ids
}

// prCIStatus mirrors the workflow's ciStatusFrom: PASS only when every check run completed
// with success, neutral or skipped; UNKNOWN while any is pending or when there are none.
func prCIStatus(runs []prCheckRun) string {
	if len(runs) == 0 {
		return "UNKNOWN"
	}
	pass := true
	for _, r := range runs {
		if r.Status != "completed" {
			return "UNKNOWN"
		}
		c := ""
		if r.Conclusion != nil {
			c = *r.Conclusion
		}
		if c != "success" && c != "neutral" && c != "skipped" {
			pass = false
		}
	}
	if pass {
		return "PASS"
	}
	return "FAIL"
}

type prCheckRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

type prLinkedIssue struct {
	Number int     `json:"number"`
	URL    string  `json:"url"`
	Title  *string `json:"title"` // nil when the issue doesn't exist or isn't readable
	State  *string `json:"state"`
	IsPR   bool    `json:"is_pull_request"` // "#N" pointing at another pull request
}

type prStatus struct {
	Repository  string  `json:"repository_full_name"`
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	URL         string  `json:"html_url"`
	State       string  `json:"state"` // open | closed
	Draft       bool    `json:"draft"`
	Merged      bool    `json:"merged"`
	MergedAt    *string `json:"merged_at"`
	AuthorLogin string  `json:"author_login"`
	AuthorID    int64   `json:"author_github_id"`
	HeadSHA     string  `json:"head_sha"`
	BaseRef     string  `json:"base_ref"`
	// What CRE counts as linked: "fixes #N" (and close/resolve forms) in the description.
	LinkedIssues []prLinkedIssue `json:"linked_issues"`
	// What GitHub shows as linked (closing keywords + the Development sidebar). Issues here
	// but not in LinkedIssues don't count for CRE. nil when GitHub couldn't be asked.
	GitHubLinkedIssues []int        `json:"github_linked_issues"`
	CIStatus           string       `json:"ci_status"` // PASS | FAIL | UNKNOWN
	Checks             []prCheckRun `json:"checks"`
	Approvals          int          `json:"approvals"`
	FetchedAt          string       `json:"fetched_at"`
}

// prGitHub reads pull requests live.
type prGitHub interface {
	PRStatus(ctx context.Context, repo string, n int) (*prStatus, error)
}

type githubPRs struct {
	api  string
	read ghapp.Tokens // pull requests, checks, metadata, issues
	slim ghapp.Tokens // without issues, for Apps lacking the Issues permission
	http *http.Client
}

func newGitHubPRs(api string, tokens ghapp.Tokens) *githubPRs {
	if api == "" {
		api = "https://api.github.com"
	}
	base := map[string]string{"pull_requests": "read", "checks": "read", "metadata": "read"}
	withIssues := map[string]string{"issues": "read"}
	for k, v := range base {
		withIssues[k] = v
	}
	return &githubPRs{
		api:  strings.TrimRight(api, "/"),
		read: ghapp.WithPermissions(tokens, withIssues),
		slim: ghapp.WithPermissions(tokens, base),
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *githubPRs) token(ctx context.Context, repo string) (string, error) {
	t, err := g.read.ForRepo(repo).Token(ctx)
	if err == nil {
		return t, nil
	}
	if t, err2 := g.slim.ForRepo(repo).Token(ctx); err2 == nil {
		return t, nil
	}
	return "", err
}

func (g *githubPRs) do(ctx context.Context, token, method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.api+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "contriboracle")
	res, err := g.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.StatusCode, fmt.Errorf("GitHub %s %s -> HTTP %d", method, path, res.StatusCode)
	}
	return res.StatusCode, json.Unmarshal(raw, out)
}

func (g *githubPRs) PRStatus(ctx context.Context, repo string, n int) (*prStatus, error) {
	token, err := g.token(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}
	var pr struct {
		Title    string  `json:"title"`
		Body     string  `json:"body"`
		HTMLURL  string  `json:"html_url"`
		State    string  `json:"state"`
		Draft    bool    `json:"draft"`
		Merged   bool    `json:"merged"`
		MergedAt *string `json:"merged_at"`
		User     struct {
			Login string `json:"login"`
			ID    int64  `json:"id"`
		} `json:"user"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if _, err := g.do(ctx, token, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), nil, &pr); err != nil {
		return nil, err
	}
	st := &prStatus{
		Repository: repo, Number: n, Title: pr.Title, URL: pr.HTMLURL, State: pr.State, Draft: pr.Draft,
		Merged: pr.Merged, MergedAt: pr.MergedAt, AuthorLogin: pr.User.Login, AuthorID: pr.User.ID,
		HeadSHA: pr.Head.SHA, BaseRef: pr.Base.Ref, Checks: []prCheckRun{},
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Everything below depends only on the PR; fetch it in parallel. Failures of the
	// optional parts leave their fields empty rather than failing the whole status.
	var wg sync.WaitGroup
	var reviewsErr, checksErr error
	wg.Add(3)
	go func() {
		defer wg.Done()
		var reviews []struct {
			State string `json:"state"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		_, reviewsErr = g.do(ctx, token, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, n), nil, &reviews)
		approvers := map[string]bool{}
		for _, r := range reviews {
			if r.State == "APPROVED" {
				approvers[r.User.Login] = true
			}
		}
		st.Approvals = len(approvers)
	}()
	go func() {
		defer wg.Done()
		var checks struct {
			CheckRuns []prCheckRun `json:"check_runs"`
		}
		_, checksErr = g.do(ctx, token, http.MethodGet, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, pr.Head.SHA), nil, &checks)
		if checks.CheckRuns != nil {
			st.Checks = checks.CheckRuns
		}
		st.CIStatus = prCIStatus(checks.CheckRuns)
	}()
	go func() {
		defer wg.Done()
		st.GitHubLinkedIssues = g.closingIssues(ctx, token, repo, n)
	}()

	ids := prLinkedIssues(pr.Body)
	st.LinkedIssues = make([]prLinkedIssue, len(ids))
	for i, id := range ids {
		st.LinkedIssues[i] = prLinkedIssue{Number: id, URL: fmt.Sprintf("https://github.com/%s/issues/%d", repo, id)}
	}
	const maxIssues = 10 // Bounds the requests; the workflow only needs one.
	for i := range st.LinkedIssues[:min(len(st.LinkedIssues), maxIssues)] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			li := &st.LinkedIssues[i]
			var is struct {
				Title       string    `json:"title"`
				State       string    `json:"state"`
				PullRequest *struct{} `json:"pull_request"`
			}
			if _, err := g.do(ctx, token, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, li.Number), nil, &is); err == nil {
				li.Title, li.State, li.IsPR = &is.Title, &is.State, is.PullRequest != nil
			}
		}()
	}
	wg.Wait()
	if reviewsErr != nil {
		return nil, reviewsErr
	}
	if checksErr != nil {
		return nil, checksErr
	}
	return st, nil
}

// closingIssues asks GitHub which issues it links to the PR; nil if it can't be asked.
func (g *githubPRs) closingIssues(ctx context.Context, token, repo string, n int) []int {
	owner, name, _ := strings.Cut(repo, "/")
	var out struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ClosingIssuesReferences struct {
						Nodes []struct {
							Number int `json:"number"`
						} `json:"nodes"`
					} `json:"closingIssuesReferences"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	query := map[string]any{
		"query":     `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){pullRequest(number:$n){closingIssuesReferences(first:20){nodes{number}}}}}`,
		"variables": map[string]any{"o": owner, "r": name, "n": n},
	}
	if _, err := g.do(ctx, token, http.MethodPost, "/graphql", query, &out); err != nil || len(out.Errors) > 0 {
		return nil
	}
	ids := []int{}
	for _, node := range out.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes {
		ids = append(ids, node.Number)
	}
	sort.Ints(ids)
	return ids
}
