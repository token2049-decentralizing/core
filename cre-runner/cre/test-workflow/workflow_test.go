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
			return jsonBody(t, map[string]any{"score": llmScore}), nil
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

// The recipient is attested: it changes the hash and reaches the settlement.
func TestRecipientWalletIsHashedAndSettled(t *testing.T) {
	mockAPIs(t, 91)
	a, _, err := run(t, request("merged"))
	require.NoError(t, err)

	withWallet := request("merged")
	withWallet["recipient_wallet"] = "7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV"
	b, rt, err := run(t, withWallet)
	require.NoError(t, err)
	require.NotEqual(t, a.EvaluationHash, b.EvaluationHash)
	require.True(t, logsContain(rt, "7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV"))
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
