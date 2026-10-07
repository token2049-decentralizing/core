package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/supabase-go"
	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

// fakeGitHubAPI is an in-process GitHub REST + GraphQL API for one repo.
type fakeGitHubAPI struct {
	t        *testing.T
	mu       sync.Mutex
	prBody   string
	merged   bool
	checks   []map[string]any
	closing  []int // GraphQL closingIssuesReferences
	issues   map[string]map[string]any
	requests []string
}

func (f *fakeGitHubAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	require.Equal(f.t, "Bearer gh-token", r.Header.Get("Authorization"))
	w := httptest.NewRecorder()
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch p := r.URL.Path; {
	case p == "/repos/acme/pool/pulls/7":
		write(map[string]any{"title": "Fix race", "body": f.prBody, "html_url": "https://github.com/acme/pool/pull/7",
			"state": "closed", "draft": false, "merged": f.merged, "merged_at": "2026-10-07T06:00:00Z",
			"user": map[string]any{"login": "alice", "id": 101}, "head": map[string]any{"sha": strings.Repeat("a", 40)},
			"base": map[string]any{"ref": "main"}})
	case p == "/repos/acme/pool/pulls/7/reviews":
		write([]any{
			map[string]any{"state": "APPROVED", "user": map[string]any{"login": "bob"}},
			map[string]any{"state": "APPROVED", "user": map[string]any{"login": "bob"}}, // Same reviewer twice.
			map[string]any{"state": "COMMENTED", "user": map[string]any{"login": "carol"}},
		})
	case strings.HasPrefix(p, "/repos/acme/pool/commits/"):
		write(map[string]any{"check_runs": f.checks})
	case strings.HasPrefix(p, "/repos/acme/pool/issues/"):
		is, ok := f.issues[strings.TrimPrefix(p, "/repos/acme/pool/issues/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			break
		}
		write(is)
	case p == "/graphql":
		body, _ := io.ReadAll(r.Body)
		require.Contains(f.t, string(body), "closingIssuesReferences")
		nodes := []any{}
		for _, n := range f.closing {
			nodes = append(nodes, map[string]any{"number": n})
		}
		write(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"closingIssuesReferences": map[string]any{"nodes": nodes}}}}})
	default:
		w.WriteHeader(http.StatusTeapot)
	}
	return w.Result(), nil
}

func newFakeGitHubPRs(t *testing.T, f *fakeGitHubAPI) *githubPRs {
	f.t = t
	g := newGitHubPRs("http://github.test", ghapp.Static("gh-token"))
	g.http = &http.Client{Transport: f}
	return g
}

func success() map[string]any { return map[string]any{"status": "completed", "conclusion": "success"} }

func TestPRLinkedIssuesMatchesWorkflow(t *testing.T) {
	// Same cases as cre/test-workflow TestGitHubHelpers.
	require.Equal(t, []int{3, 12}, prLinkedIssues("Fixes #3, closes #12 and resolved #3. See #99"))
	require.Equal(t, []int{}, prLinkedIssues("Related to #5, see acme/other#6, https://github.com/acme/pool/issues/7"))
	require.Equal(t, []int{8}, prLinkedIssues("RESOLVES #8"))
}

func TestPRCIStatusMatchesWorkflow(t *testing.T) {
	skipped, failed := "skipped", "failure"
	require.Equal(t, "UNKNOWN", prCIStatus(nil))
	require.Equal(t, "PASS", prCIStatus([]prCheckRun{{Status: "completed", Conclusion: ptr("success")}, {Status: "completed", Conclusion: &skipped}}))
	require.Equal(t, "FAIL", prCIStatus([]prCheckRun{{Status: "completed", Conclusion: &failed}}))
	require.Equal(t, "UNKNOWN", prCIStatus([]prCheckRun{{Status: "completed", Conclusion: &failed}, {Status: "in_progress"}}))
}

func ptr[T any](v T) *T { return &v }

func TestPRStatus(t *testing.T) {
	f := &fakeGitHubAPI{
		prBody: "Fixes #3. Also resolves #10, closes #404.",
		merged: true,
		checks: []any2{success(), {"status": "completed", "conclusion": "skipped"}},
		// GitHub also sees #5, linked from the Development sidebar.
		closing: []int{3, 5},
		issues: map[string]map[string]any{
			"3":  {"title": "Race in pool", "state": "open"},
			"10": {"title": "Another PR", "state": "closed", "pull_request": map[string]any{}},
		},
	}
	st, err := newFakeGitHubPRs(t, f).PRStatus(context.Background(), "acme/pool", 7)
	require.NoError(t, err)

	require.True(t, st.Merged)
	require.Equal(t, "alice", st.AuthorLogin)
	require.Equal(t, int64(101), st.AuthorID)
	require.Equal(t, strings.Repeat("a", 40), st.HeadSHA)
	require.Equal(t, "PASS", st.CIStatus)
	require.Len(t, st.Checks, 2)
	require.Equal(t, 1, st.Approvals) // Distinct approvers, like the workflow.
	require.Equal(t, []int{3, 5}, st.GitHubLinkedIssues)

	require.Len(t, st.LinkedIssues, 3)
	require.Equal(t, 3, st.LinkedIssues[0].Number)
	require.Equal(t, "Race in pool", *st.LinkedIssues[0].Title)
	require.False(t, st.LinkedIssues[0].IsPR)
	require.True(t, st.LinkedIssues[1].IsPR) // #10 is a pull request: still counted by CRE.
	require.Nil(t, st.LinkedIssues[2].Title) // #404 doesn't exist: still counted by CRE.
}

func TestPRStatusGraphQLFailureIsOptional(t *testing.T) {
	f := &fakeGitHubAPI{prBody: "no links", checks: []any2{}}
	g := newFakeGitHubPRs(t, f)
	g.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/graphql" {
			return nil, errors.New("graphql down")
		}
		return f.RoundTrip(r)
	})}
	st, err := g.PRStatus(context.Background(), "acme/pool", 7)
	require.NoError(t, err)
	require.Nil(t, st.GitHubLinkedIssues)
	require.Empty(t, st.LinkedIssues)
	require.Equal(t, "UNKNOWN", st.CIStatus)
}

type any2 = map[string]any

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeRunner records reruns instead of running them.
type fakeRunner struct {
	trigger  *prTrigger
	campaign campaignRow
	rerunOf  string
}

func (f *fakeRunner) Start(t *prTrigger, c campaignRow, rerunOf string) (string, error) {
	f.trigger, f.campaign, f.rerunOf = t, c, rerunOf
	return "33333333-3333-3333-3333-333333333333", nil
}

const rerunSourceID = "22222222-2222-2222-2222-222222222222"

func rerunExecution(status, event string, settled bool) map[string]any {
	return map[string]any{
		"id": rerunSourceID, "delivery_id": "d1", "campaign_id": testCampaignID, "repository_full_name": "acme/pool",
		"pr_number": 7, "event": event, "status": status, "settled": settled,
		"head_sha": strings.Repeat("b", 40), "author_login": "alice", "author_github_id": 101,
	}
}

func newRerunAPI(t *testing.T, f *fakePostgREST, gh *fakeGitHubAPI, runner *fakeRunner, campaigns ...campaignRow) *gin.Engine {
	r := newCampaignTestAPI(t, f) // Fakes PostgREST through http.DefaultTransport.
	db := mustDB(t)
	tools := &prTools{db: db, store: newFakeStore(campaigns...)}
	if gh != nil {
		tools.gh = newFakeGitHubPRs(t, gh)
	}
	if runner != nil {
		tools.runs = runner
	}
	registerPRTools(r, tools)
	return r
}

func TestRerunUsesCurrentHead(t *testing.T) {
	f := &fakePostgREST{execRows: []map[string]any{rerunExecution(statusFailed, "opened", false)}}
	runner := &fakeRunner{}
	gh := &fakeGitHubAPI{prBody: "Fixes #3", checks: []any2{success()}}
	r := newRerunAPI(t, f, gh, runner, campaignRow{ID: testCampaignID, RewardAsset: "USDC", MaxRewardPerPR: "100"})

	w := do(r, http.MethodPost, "/api/executions/"+rerunSourceID+"/rerun", "")
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "33333333-3333-3333-3333-333333333333")
	require.Equal(t, rerunSourceID, runner.rerunOf)
	require.Equal(t, testCampaignID, runner.campaign.ID)
	require.Equal(t, &prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "opened",
		HeadSHA: strings.Repeat("a", 40), AuthorID: 101, AuthorLogin: "alice"}, runner.trigger)
}

func TestRerunRejections(t *testing.T) {
	active := campaignRow{ID: testCampaignID, RewardAsset: "USDC", MaxRewardPerPR: "100"}
	for name, tc := range map[string]struct {
		row       map[string]any
		inFlight  int
		campaigns []campaignRow
		merged    bool
		status    int
		msg       string
	}{
		"still running":       {row: rerunExecution(statusRunning, "opened", false), campaigns: []campaignRow{active}, status: 409, msg: "still running"},
		"already settled":     {row: rerunExecution(statusCompleted, "merged", true), campaigns: []campaignRow{active}, status: 409, msg: "already settled"},
		"another in flight":   {row: rerunExecution(statusFailed, "opened", false), inFlight: 1, campaigns: []campaignRow{active}, status: 409, msg: "already queued or running"},
		"campaign not active": {row: rerunExecution(statusFailed, "opened", false), status: 409, msg: "not active"},
		"no longer merged":    {row: rerunExecution(statusFailed, "merged", false), campaigns: []campaignRow{active}, status: 409, msg: "no longer merged"},
	} {
		runner := &fakeRunner{}
		f := &fakePostgREST{execRows: []map[string]any{tc.row}, executions: tc.inFlight}
		r := newRerunAPI(t, f, &fakeGitHubAPI{prBody: "", merged: tc.merged, checks: []any2{}}, runner, tc.campaigns...)
		w := do(r, http.MethodPost, "/api/executions/"+rerunSourceID+"/rerun", "")
		require.Equal(t, tc.status, w.Code, name+": "+w.Body.String())
		require.Contains(t, w.Body.String(), tc.msg, name)
		require.Nil(t, runner.trigger, name)
	}

	// Unknown execution, and a runner without executions.
	f := &fakePostgREST{execRows: []map[string]any{}}
	require.Equal(t, http.StatusNotFound, do(newRerunAPI(t, f, nil, &fakeRunner{}), http.MethodPost, "/api/executions/"+rerunSourceID+"/rerun", "").Code)
	require.Equal(t, http.StatusNotFound, do(newRerunAPI(t, f, nil, &fakeRunner{}), http.MethodPost, "/api/executions/nope/rerun", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, do(newRerunAPI(t, f, nil, nil), http.MethodPost, "/api/executions/"+rerunSourceID+"/rerun", "").Code)
}

func TestPRStatusEndpoint(t *testing.T) {
	f := &fakePostgREST{}
	gh := &fakeGitHubAPI{prBody: "Fixes #3", merged: true, checks: []any2{success()}, issues: map[string]map[string]any{"3": {"title": "x", "state": "open"}}}
	r := newRerunAPI(t, f, gh, nil)
	w := do(r, http.MethodGet, "/api/repos/acme/pool/prs/7/status", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Data prStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.True(t, out.Data.Merged)
	require.Equal(t, 3, out.Data.LinkedIssues[0].Number)

	require.Equal(t, http.StatusBadRequest, do(r, http.MethodGet, "/api/repos/acme/pool/prs/0/status", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, do(newRerunAPI(t, f, nil, nil), http.MethodGet, "/api/repos/acme/pool/prs/7/status", "").Code)
}

// mustDB is a Supabase client for the fake PostgREST installed by newCampaignTestAPI.
func mustDB(t *testing.T) *supabase.Client {
	db, err := supabase.NewClient("http://supabase.test", "test-key", nil)
	require.NoError(t, err)
	return db
}
