package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http"
	"github.com/smartcontractkit/cre-sdk-go/cre"
)

var (
	// "closes #12", "fixes #3", "resolved #7" etc. in the PR body.
	linkedIssueRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)
	testFileRe    = regexp.MustCompile(`(?i)(^|/)(tests?|__tests__|spec)(/|$)|\.(test|spec)\.[a-z]+$|_test\.[a-z]+$`)
)

func parseLinkedIssues(body string) []int {
	seen := map[int]bool{}
	ids := []int{}
	for _, m := range linkedIssueRe.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if !seen[n] {
			seen[n] = true
			ids = append(ids, n)
		}
	}
	sort.Ints(ids)
	return ids
}

type checkRun struct {
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

func ciStatusFrom(runs []checkRun) string {
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

// GitHub API response shapes (only the fields we read).
type ghUser struct {
	Login string `json:"login"`
}
type ghPR struct {
	User  ghUser `json:"user"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Base  struct {
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Additions    int  `json:"additions"`
	Deletions    int  `json:"deletions"`
	ChangedFiles int  `json:"changed_files"`
	Merged       bool `json:"merged"`
	Labels       []struct {
		Name string `json:"name"`
	} `json:"labels"`
}
type ghReview struct {
	State string `json:"state"`
	User  ghUser `json:"user"`
}
type ghCheckRuns struct {
	CheckRuns []checkRun `json:"check_runs"`
}

type githubFetcher struct {
	sr    *http.SendRequester
	api   string
	token string
}

func (g githubFetcher) headers(extra ...string) map[string]*http.HeaderValues {
	h := map[string]*http.HeaderValues{
		"Accept":               {Values: []string{"application/vnd.github+json"}},
		"Authorization":        {Values: []string{"Bearer " + g.token}},
		"User-Agent":           {Values: []string{"contriboracle"}},
		"X-GitHub-Api-Version": {Values: []string{"2022-11-28"}},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = &http.HeaderValues{Values: []string{extra[i+1]}}
	}
	return h
}

// get starts a request; the call runs while other requests are in flight.
func (g githubFetcher) get(path string) cre.Promise[*http.Response] {
	return g.sr.SendRequest(&http.Request{Url: g.api + path, Method: "GET", MultiHeaders: g.headers()})
}

// Paths only. The REST files endpoint returns every patch and can exceed the HTTP response limit.
const filesQuery = `query($owner:String!,$name:String!,$number:Int!,$after:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    files(first:100,after:$after){nodes{path} pageInfo{hasNextPage endCursor}}}}}`

const maxFilePages = 5 // 500 files is plenty to spot tests.

type ghFilesPage struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Files struct {
					Nodes []struct {
						Path string `json:"path"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"files"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (g githubFetcher) filesPage(repository string, pr int, after string) cre.Promise[*http.Response] {
	owner, name, _ := strings.Cut(repository, "/")
	vars := map[string]any{"owner": owner, "name": name, "number": pr}
	if after != "" {
		vars["after"] = after
	}
	body, _ := json.Marshal(map[string]any{"query": filesQuery, "variables": vars})
	return g.sr.SendRequest(&http.Request{
		Url:          g.api + "/graphql",
		Method:       "POST",
		MultiHeaders: g.headers("Content-Type", "application/json"),
		Body:         body,
	})
}

// firstTestFile pages through PR file paths until a test file is found; "" if none.
func (g githubFetcher) firstTestFile(first cre.Promise[*http.Response], repository string, pr int) (string, error) {
	p := first
	for range maxFilePages {
		page, err := decode[ghFilesPage](p, "/graphql files")
		if err != nil {
			return "", err
		}
		if len(page.Errors) > 0 {
			return "", fmt.Errorf("GitHub /graphql files: %s", page.Errors[0].Message)
		}
		files := page.Data.Repository.PullRequest.Files
		for _, f := range files.Nodes {
			if testFileRe.MatchString(f.Path) {
				return f.Path, nil
			}
		}
		if !files.PageInfo.HasNextPage {
			return "", nil
		}
		p = g.filesPage(repository, pr, files.PageInfo.EndCursor)
	}
	return "", nil
}

func decode[T any](p cre.Promise[*http.Response], path string) (T, error) {
	var out T
	res, err := p.Await()
	if err != nil {
		return out, fmt.Errorf("GitHub %s: %w", path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return out, fmt.Errorf("GitHub %s -> HTTP %d", path, res.StatusCode)
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		return out, fmt.Errorf("GitHub %s: %w", path, err)
	}
	return out, nil
}

type evidenceInput struct {
	APIURL     string
	Token      string
	Repository string
	PRNumber   int
}

// fetchGitHubEvidence runs on each node. Returns JSON so nodes reach identical consensus.
func fetchGitHubEvidence(in evidenceInput, _ *slog.Logger, sr *http.SendRequester) (string, error) {
	g := githubFetcher{sr: sr, api: in.APIURL, token: in.Token}
	base := fmt.Sprintf("/repos/%s/pulls/%d", in.Repository, in.PRNumber)

	pr, err := decode[ghPR](g.get(base), base)
	if err != nil {
		return "", err
	}

	// Check runs need the head SHA, so these start together after the PR call.
	reviewsPath := base + "/reviews?per_page=100"
	checksPath := fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", in.Repository, pr.Head.SHA)
	reviewsP, checksP := g.get(reviewsPath), g.get(checksPath)
	filesP := g.filesPage(in.Repository, in.PRNumber, "")

	reviews, err := decode[[]ghReview](reviewsP, reviewsPath)
	if err != nil {
		return "", err
	}
	checks, err := decode[ghCheckRuns](checksP, checksPath)
	if err != nil {
		return "", err
	}
	testFile, err := g.firstTestFile(filesP, in.Repository, in.PRNumber)
	if err != nil {
		return "", err
	}

	approvers := map[string]bool{}
	for _, r := range reviews {
		if r.State == "APPROVED" {
			approvers[r.User.Login] = true
		}
	}
	labels := []string{}
	for _, l := range pr.Labels {
		labels = append(labels, l.Name)
	}
	sort.Strings(labels) // Stable across nodes.

	ev := GitHubEvidence{
		Repository:      in.Repository,
		PullRequest:     in.PRNumber,
		Author:          pr.User.Login,
		Title:           pr.Title,
		Body:            pr.Body,
		BaseSHA:         pr.Base.SHA,
		HeadSHA:         pr.Head.SHA,
		Additions:       pr.Additions,
		Deletions:       pr.Deletions,
		ChangedFiles:    pr.ChangedFiles,
		TestsTouched:    testFile != "",
		TestFile:        testFile,
		LinkedIssues:    parseLinkedIssues(pr.Body),
		CIStatus:        ciStatusFrom(checks.CheckRuns),
		ReviewApprovals: len(approvers),
		Merged:          pr.Merged,
		Labels:          labels,
	}
	b, err := json.Marshal(ev)
	return string(b), err
}
