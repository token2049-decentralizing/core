package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/supabase-community/supabase-go"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
	"github.com/token2049-decentralizing/core/cre-runner/internal/reviewer"
)

// Appeals: POST /api/executions/:id/appeals asks a human to review an evaluation.
// It records the request and comments on the PR, @mentioning a reviewer. Scores never change here.

const appealsTable = "cre_execution_appeals"

type appealRow struct {
	ID             string  `json:"id"`
	ExecutionID    string  `json:"execution_id"`
	Reason         string  `json:"reason"`
	MentionedLogin *string `json:"mentioned_login"`
	CommentURL     *string `json:"comment_url"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"created_at"`
}

const appealColumns = "id,execution_id,reason,mentioned_login,comment_url,status,created_at"

type pullInfo struct {
	Author   string
	Body     string
	MergedBy string
}

// appealGitHub is the GitHub access an appeal needs.
type appealGitHub interface {
	PullRequest(ctx context.Context, repo string, n int) (*pullInfo, error)
	// IssueAuthor returns "" when the issue is missing or not readable.
	IssueAuthor(ctx context.Context, repo string, n int) (string, error)
	Comment(ctx context.Context, repo string, n int, body string) (url string, err error)
}

type appeals struct {
	db               *supabase.Client
	gh               appealGitHub // nil: appeals disabled (no GitHub credentials)
	dashboardURL     string       // Links the comment back to the execution page.
	fallbackReviewer string       // Mentioned when no issue author or merger is found.
}

func registerAppeals(r *gin.Engine, a *appeals) {
	r.POST("/api/executions/:id/appeals", a.create)
}

const (
	minAppealChars = 10
	maxAppealChars = 2000
)

var githubLoginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

type appealExecution struct {
	ID                 string          `json:"id"`
	RepositoryFullName string          `json:"repository_full_name"`
	PRNumber           int             `json:"pr_number"`
	Status             string          `json:"status"`
	Score              *int            `json:"score"`
	Eligible           *bool           `json:"eligible"`
	Reward             *string         `json:"reward"`
	HeadSHA            *string         `json:"head_sha"`
	EvaluationHash     *string         `json:"evaluation_hash"`
	Scorecard          json.RawMessage `json:"scorecard"`
	Campaign           *struct {
		RewardAsset string `json:"reward_asset"`
		MinScore    *int   `json:"min_score"`
	} `json:"campaign"`
}

func (a *appeals) create(c *gin.Context) {
	id := c.Param("id")
	if !uuidRe.MatchString(id) {
		apiError(c, http.StatusNotFound, "execution not found")
		return
	}
	if a.gh == nil {
		apiError(c, http.StatusServiceUnavailable, "review requests need GitHub App credentials on the runner")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)).Decode(&body); err != nil {
		apiError(c, http.StatusBadRequest, "body must be JSON with a reason")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if n := len([]rune(reason)); n < minAppealChars || n > maxAppealChars {
		apiError(c, http.StatusBadRequest, fmt.Sprintf("reason must be %d to %d characters", minAppealChars, maxAppealChars))
		return
	}

	var execs []appealExecution
	_, err := a.db.From(executionsTable).
		Select("id,repository_full_name,pr_number,status,score,eligible,reward,head_sha,evaluation_hash,scorecard,"+
			"campaign:campaigns(reward_asset,min_score)", "", false).
		Eq("id", id).Limit(1, "").ExecuteTo(&execs)
	switch {
	case err != nil:
		internalError(c, "load execution", err)
		return
	case len(execs) == 0:
		apiError(c, http.StatusNotFound, "execution not found")
		return
	case execs[0].Status != statusCompleted:
		apiError(c, http.StatusConflict, "only completed evaluations can be reviewed")
		return
	}
	e := execs[0]

	// The unique execution_id claims the request first, so double clicks post one comment.
	var rows []appealRow
	_, err = a.db.From(appealsTable).
		Insert(map[string]any{"execution_id": id, "reason": reason}, false, "", "representation", "").
		ExecuteTo(&rows)
	switch {
	case isUniqueViolation(err):
		apiError(c, http.StatusConflict, "a review was already requested for this evaluation")
		return
	case err != nil:
		internalError(c, "record review request", err)
		return
	case len(rows) == 0:
		internalError(c, "record review request", errors.New("no row returned"))
		return
	}
	row := rows[0]
	undo := func(msg string, err error) {
		log.Printf("appeal %s: %s: %v", row.ID, msg, err)
		if _, _, derr := a.db.From(appealsTable).Delete("minimal", "").Eq("id", row.ID).Execute(); derr != nil {
			log.Printf("appeal %s: undo: %v", row.ID, derr)
		}
		apiError(c, http.StatusBadGateway, msg)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pr, err := a.gh.PullRequest(ctx, e.RepositoryFullName, e.PRNumber)
	if err != nil {
		undo("could not read the pull request on GitHub", err)
		return
	}
	reviewerLogin := a.pickReviewer(ctx, e.RepositoryFullName, pr)
	url, err := a.gh.Comment(ctx, e.RepositoryFullName, e.PRNumber, a.comment(e, reason, reviewerLogin))
	if err != nil {
		undo("GitHub refused the comment; the App needs Pull requests: Read and write", err)
		return
	}

	update := map[string]any{"comment_url": url}
	row.CommentURL = &url
	if reviewerLogin != "" {
		update["mentioned_login"] = reviewerLogin
		row.MentionedLogin = &reviewerLogin
	}
	if _, _, err := a.db.From(appealsTable).Update(update, "minimal", "").Eq("id", row.ID).Execute(); err != nil {
		log.Printf("appeal %s: record comment: %v", row.ID, err) // The comment is posted; keep the request.
	}
	c.JSON(http.StatusCreated, gin.H{"data": row})
}

// pickReviewer: whoever opened the linked issue, else whoever merged the PR, else the configured fallback.
// Never the PR author (they are the one asking) and never a bot.
func (a *appeals) pickReviewer(ctx context.Context, repo string, pr *pullInfo) string {
	ok := func(login string) bool {
		return githubLoginRe.MatchString(login) && !strings.EqualFold(login, pr.Author)
	}
	if n := reviewer.FirstLinkedIssue(pr.Body); n > 0 {
		login, err := a.gh.IssueAuthor(ctx, repo, n)
		if err != nil {
			log.Printf("appeal: issue #%d author: %v", n, err)
		}
		if ok(login) {
			return login
		}
	}
	if ok(pr.MergedBy) {
		return pr.MergedBy
	}
	if ok(a.fallbackReviewer) {
		return a.fallbackReviewer
	}
	return ""
}

// comment is the PR comment: the result, the contributor's reason and the weakest criteria. No LLM prose.
func (a *appeals) comment(e appealExecution, reason, reviewerLogin string) string {
	var b strings.Builder
	b.WriteString("### Review requested on a ContribOracle score\n\n")
	if reviewerLogin != "" {
		fmt.Fprintf(&b, "@%s, the contributor asked for a human review of this evaluation.\n\n", reviewerLogin)
	} else {
		b.WriteString("The contributor asked a maintainer of this repository to review this evaluation.\n\n")
	}

	asset, minScore := "", 0
	if e.Campaign != nil {
		asset = e.Campaign.RewardAsset
		if e.Campaign.MinScore != nil {
			minScore = *e.Campaign.MinScore
		}
	}
	result := "Not eligible"
	if e.Eligible != nil && *e.Eligible {
		result = "Eligible"
	}
	b.WriteString("| Score | Result | Reward | Commit |\n| --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| **%s** / 100 (min %d) | %s | %s | %s |\n\n",
		intOr(e.Score, "—"), minScore, result, formatBaseUnits(e.Reward, asset), shortSHA(e.HeadSHA))

	b.WriteString("**Reason**\n\n")
	for _, line := range strings.Split(neutralizeMentions(reason), "\n") {
		b.WriteString("> " + line + "\n")
	}
	if weak := weakestCriteria(e.Scorecard, 3); len(weak) > 0 {
		b.WriteString("\n**Lowest criteria**\n\n")
		for _, w := range weak {
			b.WriteString("- " + w + "\n")
		}
	}
	b.WriteString("\n")
	if a.dashboardURL != "" {
		fmt.Fprintf(&b, "[Score breakdown](%s/executions/%s)", strings.TrimRight(a.dashboardURL, "/"), e.ID)
		if e.EvaluationHash != nil {
			b.WriteString(" · ")
		}
	}
	if e.EvaluationHash != nil {
		fmt.Fprintf(&b, "evaluation hash `%s`", *e.EvaluationHash)
	}
	b.WriteString("\n\n<sub>Sent from the ContribOracle dashboard. The score stays as is until a reviewer acts.</sub>\n")
	return b.String()
}

// neutralizeMentions keeps contributor text from pinging people: "@bob" -> "@​bob".
func neutralizeMentions(s string) string {
	return strings.ReplaceAll(s, "@", "@​")
}

func intOr(n *int, def string) string {
	if n == nil {
		return def
	}
	return fmt.Sprint(*n)
}

func shortSHA(sha *string) string {
	if sha == nil || len(*sha) < 7 {
		return "—"
	}
	return "`" + (*sha)[:7] + "`"
}

// formatBaseUnits renders token base units ("445000000", USDC) as "445 USDC".
func formatBaseUnits(units *string, asset string) string {
	if units == nil {
		return "—"
	}
	n, ok := new(big.Int).SetString(*units, 10)
	dec, known := tokenDecimals[asset]
	if !ok || !known {
		return *units
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)
	whole, frac := new(big.Int).QuoRem(n, scale, new(big.Int))
	out := whole.String()
	if frac.Sign() != 0 {
		out += "." + strings.TrimRight(fmt.Sprintf("%0*s", dec, frac.String()), "0")
	}
	return out + " " + asset
}

var criterionLabel = map[string]string{
	"linked_issue": "Linked issue", "ci": "CI", "approval": "Approval", "tests": "Tests", "size": "Diff size",
}

// weakestCriteria lists the n criteria that lost the largest share of their points.
func weakestCriteria(raw json.RawMessage, n int) []string {
	var card struct {
		Evidence []struct {
			Check       string `json:"check"`
			Points, Max int
			Detail      string `json:"detail"`
		} `json:"evidence"`
		Reviews []struct {
			Persona    string `json:"persona"`
			Role       string `json:"role"`
			Categories []struct {
				Name        string `json:"name"`
				Points, Max int
			} `json:"categories"`
		} `json:"reviews"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &card) != nil {
		return nil
	}
	type item struct {
		text        string
		points, max int
	}
	var items []item
	for _, c := range card.Evidence {
		if c.Max > 0 && c.Points < c.Max {
			label := criterionLabel[c.Check]
			if label == "" {
				label = c.Check
			}
			items = append(items, item{fmt.Sprintf("%s %d/%d (%s)", label, c.Points, c.Max, c.Detail), c.Points, c.Max})
		}
	}
	for _, r := range card.Reviews {
		who := r.Persona
		if who == "" {
			who = r.Role
		}
		for _, c := range r.Categories {
			if c.Max > 0 && c.Points < c.Max {
				items = append(items, item{fmt.Sprintf("%s %d/%d (%s review)", strings.ReplaceAll(c.Name, "_", " "),
					c.Points, c.Max, who), c.Points, c.Max})
			}
		}
	}
	// Lowest share first; ties: more points lost first.
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if l, r := a.points*b.max, b.points*a.max; l != r {
			return l < r
		}
		return a.max-a.points > b.max-b.points
	})
	out := []string{}
	for _, it := range items[:min(n, len(items))] {
		out = append(out, neutralizeMentions(it.text))
	}
	return out
}

// githubAppeals talks to GitHub with a token that may comment on pull requests.
type githubAppeals struct {
	api   string
	write ghapp.Tokens // pull_requests:write + issues:read
	slim  ghapp.Tokens // pull_requests:write only, for Apps without the Issues permission
	http  *http.Client
}

func newGitHubAppeals(api string, tokens ghapp.Tokens) *githubAppeals {
	if api == "" {
		api = "https://api.github.com"
	}
	return &githubAppeals{
		api:   strings.TrimRight(api, "/"),
		write: ghapp.WithPermissions(tokens, map[string]string{"pull_requests": "write", "issues": "read", "metadata": "read"}),
		slim:  ghapp.WithPermissions(tokens, map[string]string{"pull_requests": "write", "metadata": "read"}),
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *githubAppeals) token(ctx context.Context, repo string) (string, error) {
	t, err := g.write.ForRepo(repo).Token(ctx)
	if err == nil {
		return t, nil
	}
	if t, err2 := g.slim.ForRepo(repo).Token(ctx); err2 == nil {
		return t, nil
	}
	return "", err
}

func (g *githubAppeals) do(ctx context.Context, repo, method, path string, body, out any) (int, error) {
	token, err := g.token(ctx, repo)
	if err != nil {
		return 0, fmt.Errorf("github token: %w", err)
	}
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
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.StatusCode, fmt.Errorf("GitHub %s %s -> HTTP %d: %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode, json.Unmarshal(raw, out)
}

func (g *githubAppeals) PullRequest(ctx context.Context, repo string, n int) (*pullInfo, error) {
	var pr struct {
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		MergedBy *struct {
			Login string `json:"login"`
		} `json:"merged_by"`
	}
	if _, err := g.do(ctx, repo, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), nil, &pr); err != nil {
		return nil, err
	}
	info := &pullInfo{Author: pr.User.Login, Body: pr.Body}
	if pr.MergedBy != nil {
		info.MergedBy = pr.MergedBy.Login
	}
	return info, nil
}

func (g *githubAppeals) IssueAuthor(ctx context.Context, repo string, n int) (string, error) {
	var is struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	status, err := g.do(ctx, repo, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, n), nil, &is)
	if status == http.StatusNotFound || status == http.StatusForbidden {
		return "", nil
	}
	return is.User.Login, err
}

func (g *githubAppeals) Comment(ctx context.Context, repo string, n int, body string) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	_, err := g.do(ctx, repo, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, n),
		map[string]string{"body": body}, &out)
	return out.HTMLURL, err
}
