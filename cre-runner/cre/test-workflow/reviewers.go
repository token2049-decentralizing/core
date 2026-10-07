package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http"
	"github.com/smartcontractkit/cre-sdk-go/cre"
	"google.golang.org/protobuf/types/known/durationpb"
)

// reviewerRequestBody is sent to every reviewer. PR text is passed as data, never as instructions.
func reviewerRequestBody(e GitHubEvidence) map[string]any {
	return map[string]any{
		"repository":    e.Repository,
		"pr_number":     e.PullRequest,
		"base_sha":      e.BaseSHA,
		"head_sha":      e.HeadSHA,
		"linked_issues": e.LinkedIssues,
		"untrusted":     map[string]any{"title": e.Title, "body": e.Body},
	}
}

// Under the 10s simulator HTTP limit. The runner warms the reviewer cache first, so this is a cache hit.
const reviewerTimeout = 9 * time.Second

type reviewerCall struct {
	URL    string // Empty = stub (uses StubScore).
	APIKey string
}

type reviewInput struct {
	CodeReviewer reviewerCall
	LLM          reviewerCall
	Evidence     GitHubEvidence
	StubScore    int
}

// Each node's scores; nodes agree on the median of each score. Details (rubric + findings, canonical
// JSON) must be identical: the reviewer serves one cached review per commit, so every node gets the same.
type reviewScores struct {
	CodeReviewer       int    `consensus_aggregation:"median"`
	LLM                int    `consensus_aggregation:"median"`
	CodeReviewerDetail string `consensus_aggregation:"identical"`
	LLMDetail          string `consensus_aggregation:"identical"`
}

// reviewDetail is the optional rubric and findings a reviewer returns next to its score.
type reviewDetail struct {
	Persona    string          `json:"persona"`
	Categories []CategoryScore `json:"categories"`
	Findings   []Finding       `json:"findings"`
}

// Reviewer output is external input: bound it before it reaches the scorecard and the hash.
const (
	maxCategories = 8
	maxFindings   = 5
	maxNoteChars  = 300
)

var (
	findingFileRe  = regexp.MustCompile(`^[^\x00-\x1f]{1,300}$`)
	findingLinesRe = regexp.MustCompile(`^[1-9][0-9]{0,5}(-[1-9][0-9]{0,5})?$`)
	nameRe         = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

func clip(s string, n int) string {
	if r := []rune(strings.TrimSpace(s)); len(r) > n {
		return string(r[:n])
	}
	return strings.TrimSpace(s)
}

// detailJSON validates a reviewer reply's breakdown and findings; "" when it has none.
func detailJSON(body []byte) (string, error) {
	var out struct {
		Persona   string          `json:"persona"`
		Breakdown []CategoryScore `json:"breakdown"`
		Findings  []struct {
			Severity    string `json:"severity"`
			Category    string `json:"category"`
			File        string `json:"file"`
			Lines       string `json:"lines"`
			Description string `json:"description"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Breakdown) == 0 {
		return "", nil // Plain {"score": n} reviewer: no detail.
	}
	d := reviewDetail{Categories: []CategoryScore{}, Findings: []Finding{}}
	if nameRe.MatchString(out.Persona) {
		d.Persona = out.Persona
	}
	for _, c := range out.Breakdown[:min(len(out.Breakdown), maxCategories)] {
		if !nameRe.MatchString(c.Name) || c.Max <= 0 || c.Max > 100 {
			continue
		}
		d.Categories = append(d.Categories, CategoryScore{Name: c.Name, Points: max(0, min(c.Max, c.Points)), Max: c.Max})
	}
	for _, f := range out.Findings[:min(len(out.Findings), maxFindings)] {
		fd := Finding{Persona: d.Persona, Severity: "low", Note: clip(f.Description, maxNoteChars)}
		if f.Severity == "high" || f.Severity == "medium" {
			fd.Severity = f.Severity
		}
		if nameRe.MatchString(f.Category) {
			fd.Category = f.Category
		}
		if findingFileRe.MatchString(f.File) {
			fd.File = f.File
			if findingLinesRe.MatchString(f.Lines) {
				fd.Lines = f.Lines
			}
		}
		if fd.Note != "" {
			d.Findings = append(d.Findings, fd)
		}
	}
	b, err := canonicalJSON(d)
	return string(b), err
}

func startReview(sr *http.SendRequester, c reviewerCall, body []byte) cre.Promise[*http.Response] {
	if c.URL == "" {
		return nil
	}
	return sr.SendRequest(&http.Request{
		Url:    c.URL,
		Method: "POST",
		MultiHeaders: map[string]*http.HeaderValues{
			"Content-Type":  {Values: []string{"application/json"}},
			"Authorization": {Values: []string{"Bearer " + c.APIKey}},
		},
		Body:          body,
		Timeout:       durationpb.New(reviewerTimeout),
		CacheSettings: &http.CacheSettings{Store: false}, // POST: never reuse a cached response.
	})
}

// awaitScore expects the reviewer to reply { "score": 0-100 }, optionally with
// "persona", "breakdown" and "findings" (internal/reviewer), returned as canonical detail JSON.
func awaitScore(p cre.Promise[*http.Response], c reviewerCall, stub int) (int, string, error) {
	if p == nil {
		return stub, "", nil
	}
	res, err := p.Await()
	if err != nil {
		return 0, "", fmt.Errorf("reviewer %s: %w", c.URL, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return 0, "", fmt.Errorf("reviewer %s -> HTTP %d", c.URL, res.StatusCode)
	}
	var out struct {
		Score *float64 `json:"score"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil || out.Score == nil {
		return 0, "", fmt.Errorf("reviewer %s returned no numeric score", c.URL)
	}
	detail, err := detailJSON(res.Body)
	if err != nil {
		return 0, "", fmt.Errorf("reviewer %s detail: %w", c.URL, err)
	}
	return clamp(int(math.Round(*out.Score))), detail, nil
}

// runReviews runs on each node. Both reviewers are called in parallel.
func runReviews(in reviewInput, _ *slog.Logger, sr *http.SendRequester) (reviewScores, error) {
	body, err := json.Marshal(reviewerRequestBody(in.Evidence))
	if err != nil {
		return reviewScores{}, err
	}
	codeP := startReview(sr, in.CodeReviewer, body)
	llmP := startReview(sr, in.LLM, body)

	code, codeDetail, err := awaitScore(codeP, in.CodeReviewer, in.StubScore)
	if err != nil {
		return reviewScores{}, err
	}
	llm, llmDetail, err := awaitScore(llmP, in.LLM, in.StubScore)
	if err != nil {
		return reviewScores{}, err
	}
	return reviewScores{CodeReviewer: code, LLM: llm, CodeReviewerDetail: codeDetail, LLMDetail: llmDetail}, nil
}
