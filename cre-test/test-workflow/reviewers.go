package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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

// Each node's scores; nodes agree on the median of each field.
type reviewScores struct {
	CodeReviewer int `consensus_aggregation:"median"`
	LLM          int `consensus_aggregation:"median"`
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

// awaitScore expects the reviewer to reply { "score": 0-100 }.
// TODO: adapt request/response to the real reviewer and LLM APIs.
func awaitScore(p cre.Promise[*http.Response], c reviewerCall, stub int) (int, error) {
	if p == nil {
		return stub, nil
	}
	res, err := p.Await()
	if err != nil {
		return 0, fmt.Errorf("reviewer %s: %w", c.URL, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return 0, fmt.Errorf("reviewer %s -> HTTP %d", c.URL, res.StatusCode)
	}
	var out struct {
		Score *float64 `json:"score"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil || out.Score == nil {
		return 0, fmt.Errorf("reviewer %s returned no numeric score", c.URL)
	}
	return clamp(int(math.Round(*out.Score))), nil
}

// runReviews runs on each node. Both reviewers are called in parallel.
func runReviews(in reviewInput, _ *slog.Logger, sr *http.SendRequester) (reviewScores, error) {
	body, err := json.Marshal(reviewerRequestBody(in.Evidence))
	if err != nil {
		return reviewScores{}, err
	}
	codeP := startReview(sr, in.CodeReviewer, body)
	llmP := startReview(sr, in.LLM, body)

	code, err := awaitScore(codeP, in.CodeReviewer, in.StubScore)
	if err != nil {
		return reviewScores{}, err
	}
	llm, err := awaitScore(llmP, in.LLM, in.StubScore)
	if err != nil {
		return reviewScores{}, err
	}
	return reviewScores{CodeReviewer: code, LLM: llm}, nil
}
