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
	cfg := &Config{GitHubAPIURL: apiURL, Campaign: testPolicy}
	cfg.Reviewers.CodeReviewer = ReviewerConfig{SecretID: "CODE_REVIEWER_API_KEY"}
	cfg.Reviewers.LLM = ReviewerConfig{URL: llmURL, SecretID: "LLM_API_KEY"}
	return cfg
}

var testSecrets = testutils.Secrets{"main": {"GITHUB_TOKEN": "gh-token", "LLM_API_KEY": "llm-key"}}

func jsonBody(t *testing.T, v any) *http.Response {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return &http.Response{StatusCode: 200, Body: b}
}

// mockAPIs fakes GitHub and the LLM reviewer; returns the URLs called.
func mockAPIs(t *testing.T, llmScore int) *[]string {
	mock, err := httpmock.NewClientCapability(t)
	require.NoError(t, err)

	var mu sync.Mutex
	urls := &[]string{}
	mock.SendRequest = func(_ context.Context, req *http.Request) (*http.Response, error) {
		mu.Lock()
		*urls = append(*urls, req.Url)
		mu.Unlock()

		if req.Url == llmURL {
			require.Equal(t, "Bearer llm-key", req.Headers["Authorization"])
			require.NotContains(t, string(req.Body), "llm-key")
			return jsonBody(t, map[string]any{"score": llmScore}), nil
		}
		require.Equal(t, "Bearer gh-token", req.Headers["Authorization"])

		path := strings.TrimPrefix(req.Url, apiURL)
		switch {
		case path == "/repos/acme/pool/pulls/102":
			return jsonBody(t, map[string]any{
				"user": map[string]any{"login": "alice"}, "title": "Fix connection pool race condition",
				"body": "Fixes #384", "base": map[string]any{"sha": "b"}, "head": map[string]any{"sha": "h"},
				"additions": 42, "deletions": 17, "changed_files": 3, "merged": true, "labels": []any{},
			}), nil
		case strings.HasPrefix(path, "/repos/acme/pool/pulls/102/reviews"):
			return jsonBody(t, []any{map[string]any{"state": "APPROVED", "user": map[string]any{"login": "bob"}}}), nil
		case strings.HasPrefix(path, "/repos/acme/pool/pulls/102/files"):
			return jsonBody(t, []any{map[string]any{"filename": "src/pool.go"}, map[string]any{"filename": "src/pool_test.go"}}), nil
		case strings.HasPrefix(path, "/repos/acme/pool/commits/h/check-runs"):
			return jsonBody(t, map[string]any{"check_runs": []any{map[string]any{"status": "completed", "conclusion": "success"}}}), nil
		}
		return &http.Response{StatusCode: 404, Body: []byte(`{}`)}, nil
	}
	return urls
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

func TestEmptySecretFailsClearly(t *testing.T) {
	mockAPIs(t, 91)
	rt := testutils.NewRuntime(t, testutils.Secrets{"main": {"GITHUB_TOKEN": ""}})
	_, err := onHTTPTrigger(testConfig(), rt, payload(t, request("opened")))
	require.ErrorContains(t, err, "secret GITHUB_TOKEN is empty")
}
