package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http"
	httpmock "github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http/mock"
	"github.com/smartcontractkit/cre-sdk-go/cre/testutils"
	"github.com/stretchr/testify/require"
)

const apiURL = "https://api.github.test"
const llmURL = "https://llm.test/score"

func testConfig() *Config {
	cfg := &Config{Mode: ModeSimulation, GitHubAPIURL: apiURL, Campaign: testPolicy}
	cfg.Reviewers.CodeReviewer = ReviewerConfig{SecretID: "REVIEWER_TOKEN"}
	cfg.Reviewers.LLM = ReviewerConfig{URL: llmURL, SecretID: "REVIEWER_TOKEN"}
	return cfg
}

var testSecrets = testutils.Secrets{"main": {"GITHUB_TOKEN": "gh-token", "REVIEWER_TOKEN": "rev-token"}}

const headSHA = "1111111111111111111111111111111111111111"

func header(req *http.Request, name string) string {
	if v := req.MultiHeaders[name]; v != nil && len(v.Values) > 0 {
		return v.Values[0]
	}
	return ""
}

func jsonBody(t *testing.T, v any) *http.Response {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return &http.Response{StatusCode: 200, Body: b}
}

// mockAPIs fakes GitHub and the LLM reviewer; returns the URLs called.
func mockAPIs(t *testing.T, llmScore int) *[]string {
	return mockAPIsWith(t, map[string]any{"score": llmScore})
}

// mockAPIsWith fakes GitHub and an LLM reviewer that replies llmReply.
func mockAPIsWith(t *testing.T, llmReply map[string]any) *[]string {
	mock, err := httpmock.NewClientCapability(t)
	require.NoError(t, err)

	var mu sync.Mutex
	urls := &[]string{}
	mock.SendRequest = func(_ context.Context, req *http.Request) (*http.Response, error) {
		mu.Lock()
		*urls = append(*urls, req.Url)
		mu.Unlock()

		if req.Url == llmURL {
			require.Equal(t, "Bearer rev-token", header(req, "Authorization"))
			require.NotContains(t, string(req.Body), "rev-token")
			require.False(t, req.CacheSettings.GetStore())
			require.NotNil(t, req.Timeout)
			return jsonBody(t, llmReply), nil
		}
		require.Equal(t, "Bearer gh-token", header(req, "Authorization"))

		path := strings.TrimPrefix(req.Url, apiURL)
		switch {
		case path == "/repos/acme/pool/pulls/102":
			return jsonBody(t, map[string]any{
				"user": map[string]any{"login": "alice"}, "title": "Fix connection pool race condition",
				"body": "Fixes #384", "base": map[string]any{"sha": "b"}, "head": map[string]any{"sha": headSHA},
				"additions": 42, "deletions": 17, "changed_files": 3, "merged": true,
				"labels": []any{map[string]any{"name": "zeta"}, map[string]any{"name": "alpha"}},
			}), nil
		case strings.HasPrefix(path, "/repos/acme/pool/pulls/102/reviews"):
			return jsonBody(t, []any{map[string]any{"state": "APPROVED", "user": map[string]any{"login": "bob"}}}), nil
		case path == "/graphql":
			require.Equal(t, "POST", req.Method)
			var q struct {
				Variables map[string]any `json:"variables"`
			}
			require.NoError(t, json.Unmarshal(req.Body, &q))
			require.Equal(t, "acme", q.Variables["owner"])
			// Page 1 has no tests; page 2 does: proves pagination.
			if q.Variables["after"] == nil {
				return jsonBody(t, filesPage([]string{"src/pool.go"}, true, "c1")), nil
			}
			require.Equal(t, "c1", q.Variables["after"])
			return jsonBody(t, filesPage([]string{"src/pool_test.go"}, false, "")), nil
		case strings.HasPrefix(path, "/repos/acme/pool/commits/"+headSHA+"/check-runs"):
			return jsonBody(t, map[string]any{"check_runs": []any{map[string]any{"status": "completed", "conclusion": "success"}}}), nil
		}
		return &http.Response{StatusCode: 404, Body: []byte(`{}`)}, nil
	}
	return urls
}

func filesPage(paths []string, more bool, cursor string) map[string]any {
	nodes := []any{}
	for _, p := range paths {
		nodes = append(nodes, map[string]any{"path": p})
	}
	return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"files": map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}},
	}}}}
}

func payload(t *testing.T, v any) *http.Payload {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return &http.Payload{Input: b}
}

func request(event string) map[string]any {
	return map[string]any{"repository": "acme/pool", "pr_number": 102, "campaign_id": "example-oss-2026", "event": event}
}

func run(t *testing.T, req map[string]any) (EvaluationResponse, *testutils.TestRuntime, error) {
	runtime := testutils.NewRuntime(t, testSecrets)
	out, err := onHTTPTrigger(testConfig(), runtime, payload(t, req))
	var res EvaluationResponse
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &res))
	}
	return res, runtime, err
}

func logsContain(rt *testutils.TestRuntime, s string) bool {
	for _, l := range rt.GetLogs() {
		if strings.Contains(string(l), s) {
			return true
		}
	}
	return false
}

func TestInitWorkflow(t *testing.T) {
	wf, err := InitWorkflow(testConfig(), nil, nil)
	require.NoError(t, err)
	require.Len(t, wf, 1)
	require.Equal(t, http.Trigger(&http.Config{}).CapabilityID(), wf[0].CapabilityID())
}

func TestMergedPREndToEnd(t *testing.T) {
	urls := mockAPIs(t, 91)
	res, rt, err := run(t, request("merged"))
	require.NoError(t, err)

	// evidence 100*0.4 + stub reviewer 100*0.3 + llm 91*0.3 = 97.3 -> 97
	require.Equal(t, 97, res.Score)
	require.True(t, res.Eligible)
	require.Equal(t, "485000000", res.Reward)
	require.Regexp(t, `^0x[0-9a-f]{64}$`, res.EvaluationHash)
	require.Contains(t, *urls, llmURL)
	require.True(t, logsContain(rt, "[solana stub]"))
}

func TestNotesDoNotChangeHash(t *testing.T) {
	mockAPIs(t, 91)
	a, _, err := run(t, request("opened"))
	require.NoError(t, err)

	withNotes := request("opened")
	withNotes["notes"] = map[string]any{"x": 1}
	b, _, err := run(t, withNotes)
	require.NoError(t, err)
	require.Equal(t, a.EvaluationHash, b.EvaluationHash)
}

func TestOpenedNeverSettles(t *testing.T) {
	mockAPIs(t, 91)
	_, rt, err := run(t, request("opened"))
	require.NoError(t, err)
	require.False(t, logsContain(rt, "[solana stub]"))
}

func TestUnknownCampaign(t *testing.T) {
	mockAPIs(t, 91)
	req := request("merged")
	req["campaign_id"] = "other"
	_, _, err := run(t, req)
	require.ErrorContains(t, err, "unknown campaign")
}

func TestHeadSHAPinning(t *testing.T) {
	mockAPIs(t, 91)
	req := request("merged")
	req["head_sha"] = headSHA
	_, _, err := run(t, req)
	require.NoError(t, err)

	req["head_sha"] = strings.Repeat("2", 40)
	_, _, err = run(t, req)
	require.ErrorContains(t, err, "PR head moved")
}

func TestGraphQLErrorsSurface(t *testing.T) {
	mock, err := httpmock.NewClientCapability(t)
	require.NoError(t, err)
	mock.SendRequest = func(_ context.Context, req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.Url, "/graphql") {
			return jsonBody(t, map[string]any{"errors": []any{map[string]any{"message": "Resource not accessible"}}}), nil
		}
		if strings.HasSuffix(req.Url, "/pulls/102") {
			return jsonBody(t, map[string]any{"head": map[string]any{"sha": headSHA}}), nil
		}
		if strings.Contains(req.Url, "/check-runs") {
			return jsonBody(t, map[string]any{"check_runs": []any{}}), nil
		}
		return jsonBody(t, []any{}), nil // reviews
	}
	_, _, err = run(t, request("opened"))
	require.ErrorContains(t, err, "Resource not accessible")
}

func TestEmptySecretFailsClearly(t *testing.T) {
	mockAPIs(t, 91)
	rt := testutils.NewRuntime(t, testutils.Secrets{"main": {"GITHUB_TOKEN": ""}})
	_, err := onHTTPTrigger(testConfig(), rt, payload(t, request("opened")))
	require.ErrorContains(t, err, "secret GITHUB_TOKEN is empty")
}

func TestScorecard(t *testing.T) {
	mockAPIsWith(t, map[string]any{
		"score": 80, "persona": "issue",
		"breakdown": []any{
			map[string]any{"name": "issue_relevance", "points": 45, "max": 50},
			map[string]any{"name": "value", "points": 20, "max": 30},
			map[string]any{"name": "scope", "points": 15, "max": 20},
		},
		"findings": []any{map[string]any{"severity": "medium", "category": "value", "file": "src/pool.go",
			"lines": "88-104", "description": "Retry path has no test"}},
		"summary": "LLM prose stays out of the scorecard",
	})
	res, _, err := run(t, request("merged"))
	require.NoError(t, err)
	card := res.Scorecard
	require.NotNil(t, card)

	require.Equal(t, Weights{Evidence: 4000, CodeReviewer: 3000, LLM: 3000}, card.Weights)
	require.Equal(t, []EvidenceCheck{
		{Check: "linked_issue", Points: 25, Max: 25, Detail: "#384"},
		{Check: "ci", Points: 25, Max: 25, Detail: "all checks passed"},
		{Check: "approval", Points: 20, Max: 20, Detail: "1 approving review(s)"},
		{Check: "tests", Points: 20, Max: 20, Detail: "src/pool_test.go"},
		{Check: "size", Points: 10, Max: 10, Detail: "+42 / -17 lines"},
	}, card.Evidence)

	require.Len(t, card.Reviews, 2)
	require.Equal(t, ReviewCard{Role: "code_reviewer", Provider: "code-reviewer-stub", Score: 100, Categories: []CategoryScore{}}, card.Reviews[0])
	require.Equal(t, ReviewCard{Role: "llm", Persona: "issue", Provider: "llm", Score: 80, Categories: []CategoryScore{
		{Name: "issue_relevance", Points: 45, Max: 50}, {Name: "value", Points: 20, Max: 30}, {Name: "scope", Points: 15, Max: 20},
	}}, card.Reviews[1])
	require.Equal(t, []Finding{{Persona: "issue", Severity: "medium", Category: "value", File: "src/pool.go",
		Lines: "88-104", Note: "Retry path has no test"}}, card.Findings)

	require.Equal(t, []Gate{
		{Gate: "merged", Required: true, Passed: true},
		{Gate: "ci_passed", Required: true, Passed: true},
		{Gate: "linked_issue", Required: true, Passed: true},
		{Gate: "min_score", Required: true, Passed: true, Detail: "94 / 70"},
	}, card.Gates)
	require.Equal(t, 94, res.Score) // 100*0.4 + 100*0.3 + 80*0.3

	// The card is part of the evaluation: same score, different rubric, different hash.
	t.Run("detail changes hash", func(t *testing.T) {
		mockAPIsWith(t, map[string]any{"score": 80, "persona": "issue",
			"breakdown": []any{map[string]any{"name": "value", "points": 20, "max": 30}}})
		other, _, err := run(t, request("merged"))
		require.NoError(t, err)
		require.Equal(t, res.Score, other.Score)
		require.NotEqual(t, res.EvaluationHash, other.EvaluationHash)
	})
}

func TestDetailJSONBoundsReviewerOutput(t *testing.T) {
	d, err := detailJSON([]byte(`{"score": 50}`))
	require.NoError(t, err)
	require.Empty(t, d) // Plain score reviewer.

	long := strings.Repeat("x", 500)
	d, err = detailJSON([]byte(`{"score":50,"persona":"Code; DROP","breakdown":[
	  {"name":"tests","points":99,"max":25},{"name":"Bad Name","points":1,"max":5},{"name":"neg","points":-3,"max":10}],
	  "findings":[{"severity":"critical","category":"tests","file":"a.go","lines":"x","description":"` + long + `"},
	              {"severity":"high","description":"  "}]}`))
	require.NoError(t, err)
	var got reviewDetail
	require.NoError(t, json.Unmarshal([]byte(d), &got))
	require.Empty(t, got.Persona)
	require.Equal(t, []CategoryScore{{Name: "tests", Points: 25, Max: 25}, {Name: "neg", Points: 0, Max: 10}}, got.Categories)
	require.Len(t, got.Findings, 1)
	require.Equal(t, "low", got.Findings[0].Severity)
	require.Equal(t, "a.go", got.Findings[0].File)
	require.Empty(t, got.Findings[0].Lines)
	require.Len(t, got.Findings[0].Note, maxNoteChars)
}
