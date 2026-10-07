package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

type prInfo struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type issueInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type github struct {
	api    string
	tokens ghapp.Tokens
	http   *http.Client
}

var errNotFound = errors.New("not found")

// errDiffTooLarge: GitHub refuses diffs over its size limits; the review continues without one.
var errDiffTooLarge = errors.New("diff too large for GitHub to render")

func (g *github) get(ctx context.Context, repo, path, accept string, limit int64) ([]byte, int, error) {
	token, err := g.tokens.ForRepo(repo).Token(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("github token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "contriboracle-reviewer")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := g.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit))
	return b, res.StatusCode, err
}

func (g *github) getJSON(ctx context.Context, repo, path string, out any) error {
	b, status, err := g.get(ctx, repo, path, "application/vnd.github+json", 4<<20)
	switch {
	case err != nil:
		return err
	case status == http.StatusNotFound:
		return fmt.Errorf("GitHub %s: %w", path, errNotFound)
	case status < 200 || status > 299:
		return fmt.Errorf("GitHub %s -> HTTP %d", path, status)
	}
	return json.Unmarshal(b, out)
}

func (g *github) pr(ctx context.Context, repo string, n int) (*prInfo, error) {
	var pr prInfo
	return &pr, g.getJSON(ctx, repo, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), &pr)
}

// diff reads at most maxBytes of the unified diff.
func (g *github) diff(ctx context.Context, repo string, n int, maxBytes int64) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, n)
	b, status, err := g.get(ctx, repo, path, "application/vnd.github.diff", maxBytes)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusNotAcceptable || status == http.StatusUnprocessableEntity:
		return nil, errDiffTooLarge
	case status < 200 || status > 299:
		return nil, fmt.Errorf("GitHub %s diff -> HTTP %d", path, status)
	}
	return b, nil
}

func (g *github) issue(ctx context.Context, repo string, n int) (*issueInfo, error) {
	var is issueInfo
	return &is, g.getJSON(ctx, repo, fmt.Sprintf("/repos/%s/issues/%d", repo, n), &is)
}

// Same pattern the workflow uses for linked issues.
var linkedIssueRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)

func firstLinkedIssue(body string) int {
	if m := linkedIssueRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func newGitHub(api string, tokens ghapp.Tokens) *github {
	if api == "" {
		api = "https://api.github.com"
	}
	return &github{api: api, tokens: tokens, http: &http.Client{Timeout: 30 * time.Second}}
}
