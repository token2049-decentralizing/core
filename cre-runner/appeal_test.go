package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/supabase-go"
)

const appealExecID = "22222222-2222-2222-2222-222222222222"

type fakeAppealGitHub struct {
	pr          pullInfo
	issueAuthor map[int]string
	commentErr  error
	comments    []string
}

func (g *fakeAppealGitHub) PullRequest(context.Context, string, int) (*pullInfo, error) {
	pr := g.pr
	return &pr, nil
}

func (g *fakeAppealGitHub) IssueAuthor(_ context.Context, _ string, n int) (string, error) {
	return g.issueAuthor[n], nil
}

func (g *fakeAppealGitHub) Comment(_ context.Context, repo string, n int, body string) (string, error) {
	if g.commentErr != nil {
		return "", g.commentErr
	}
	g.comments = append(g.comments, body)
	return "https://github.com/" + repo + "/pull/7#issuecomment-1", nil
}

func scoredExecutionRow() map[string]any {
	row := fakeExecutionRow()
	row["head_sha"] = "82d492411decc5102e8ba22317d61efd53567759"
	row["campaign"] = map[string]any{"id": testCampaignID, "name": "Bug bash", "reward_asset": "USDC", "min_score": 70}
	row["scorecard"] = map[string]any{
		"evidence": []any{
			map[string]any{"check": "approval", "points": 0, "max": 20, "detail": "no approving review"},
			map[string]any{"check": "ci", "points": 25, "max": 25, "detail": "all checks passed"},
			map[string]any{"check": "size", "points": 5, "max": 10, "detail": "+1200 / -300 lines"},
		},
		"reviews": []any{map[string]any{"role": "code_reviewer", "persona": "code", "categories": []any{
			map[string]any{"name": "tests", "points": 8, "max": 25},
			map[string]any{"name": "security", "points": 10, "max": 10},
		}}},
	}
	return row
}

func newAppealTestAPI(t *testing.T, f *fakePostgREST, gh appealGitHub) *gin.Engine {
	r := newCampaignTestAPI(t, f) // Routes PostgREST calls to f.
	db, err := supabase.NewClient("http://supabase.test", "test-key", nil)
	require.NoError(t, err)
	registerAppeals(r, &appeals{db: db, gh: gh, dashboardURL: "https://dash.test/"})
	return r
}

func TestAppealCommentsAndMentionsIssueAuthor(t *testing.T) {
	f := &fakePostgREST{execRows: []map[string]any{scoredExecutionRow()}}
	gh := &fakeAppealGitHub{
		pr:          pullInfo{Author: "alice", Body: "Fixes #12", MergedBy: "bob"},
		issueAuthor: map[int]string{12: "carol"},
	}
	r := newAppealTestAPI(t, f, gh)

	w := do(r, http.MethodPost, "/api/executions/"+appealExecID+"/appeals",
		`{"reason":"Tests are in pool_test.go, @everyone please look\nsecond line"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	require.Len(t, gh.comments, 1)
	c := gh.comments[0]
	require.Contains(t, c, "@carol, the contributor asked for a human review")
	require.Contains(t, c, "| **46** / 100 (min 70) | Not eligible | 0 USDC | `82d4924` |")
	require.Contains(t, c, "> Tests are in pool_test.go, @​everyone please look\n> second line\n")
	// Weakest share first: approval 0/20, tests 8/25, size 5/10. Full marks are left out.
	require.Contains(t, c, "- Approval 0/20 (no approving review)\n- tests 8/25 (code review)\n- Diff size 5/10 (+1200 / -300 lines)\n")
	require.NotContains(t, c, "security")
	require.Contains(t, c, "[Score breakdown](https://dash.test/executions/"+appealExecID+")")

	var out struct{ Data appealRow }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "carol", *out.Data.MentionedLogin)
	require.Equal(t, "carol", f.appeals[0]["mentioned_login"])
	require.Contains(t, f.appeals[0]["comment_url"], "issuecomment-1")

	// One request per evaluation.
	w = do(r, http.MethodPost, "/api/executions/"+appealExecID+"/appeals", `{"reason":"Asking again, louder"}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Len(t, gh.comments, 1)

	// The detail API shows the request.
	w = do(r, http.MethodGet, "/api/executions/"+appealExecID, "")
	require.Equal(t, http.StatusOK, w.Code)
	var detail struct {
		Data struct {
			Scorecard map[string]any `json:"scorecard"`
			Appeal    *appealRow     `json:"appeal"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Data.Appeal)
	require.Equal(t, "open", detail.Data.Appeal.Status)
	require.Len(t, detail.Data.Scorecard["evidence"], 3)
}

func TestAppealReviewerFallbacks(t *testing.T) {
	a := &appeals{fallbackReviewer: "maintainer"}
	pick := func(pr pullInfo, issues map[int]string) string {
		a.gh = &fakeAppealGitHub{issueAuthor: issues}
		return a.pickReviewer(context.Background(), "acme/pool", &pr)
	}
	require.Equal(t, "carol", pick(pullInfo{Author: "alice", Body: "closes #3", MergedBy: "bob"}, map[int]string{3: "carol"}))
	// The contributor opened the issue themselves: ask whoever merged.
	require.Equal(t, "bob", pick(pullInfo{Author: "alice", Body: "closes #3", MergedBy: "bob"}, map[int]string{3: "Alice"}))
	require.Equal(t, "bob", pick(pullInfo{Author: "alice", MergedBy: "bob"}, nil))
	require.Equal(t, "maintainer", pick(pullInfo{Author: "alice", MergedBy: "dependabot[bot]"}, nil))
	a.fallbackReviewer = ""
	require.Equal(t, "", pick(pullInfo{Author: "alice"}, nil))
}

func TestAppealValidationAndRollback(t *testing.T) {
	f := &fakePostgREST{execRows: []map[string]any{scoredExecutionRow()}}
	gh := &fakeAppealGitHub{pr: pullInfo{Author: "alice"}, commentErr: errors.New("HTTP 403")}
	r := newAppealTestAPI(t, f, gh)
	path := "/api/executions/" + appealExecID + "/appeals"

	require.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, path, `{"reason":"short"}`).Code)
	require.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, path, `{"reason":"`+strings.Repeat("x", 2001)+`"}`).Code)
	require.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, path, `not json`).Code)
	require.Equal(t, http.StatusNotFound, do(r, http.MethodPost, "/api/executions/nope/appeals", `{}`).Code)

	// GitHub refuses the comment: the request is undone so it can be retried.
	w := do(r, http.MethodPost, path, `{"reason":"The CI check was flaky, please re-check"}`)
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "Pull requests: Read and write")
	require.Empty(t, f.appeals)

	f.execRows[0]["status"] = "running"
	require.Equal(t, http.StatusConflict, do(r, http.MethodPost, path, `{"reason":"Not done yet but I disagree"}`).Code)

	f.execRows = []map[string]any{}
	require.Equal(t, http.StatusNotFound, do(r, http.MethodPost, path, `{"reason":"Missing execution here"}`).Code)

	disabled := newAppealTestAPI(t, &fakePostgREST{}, nil)
	require.Equal(t, http.StatusServiceUnavailable, do(disabled, http.MethodPost, path, `{"reason":"No GitHub creds here"}`).Code)
}

func TestFormatBaseUnits(t *testing.T) {
	s := func(v string) *string { return &v }
	require.Equal(t, "445 USDC", formatBaseUnits(s("445000000"), "USDC"))
	require.Equal(t, "0.05 SOL", formatBaseUnits(s("50000000"), "SOL"))
	require.Equal(t, "0 USDC", formatBaseUnits(s("0"), "USDC"))
	require.Equal(t, "—", formatBaseUnits(nil, "USDC"))
}
