package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeExecutionRow() map[string]any {
	return map[string]any{
		"id": "22222222-2222-2222-2222-222222222222", "delivery_id": "d1", "campaign_id": testCampaignID,
		"campaign":             map[string]any{"id": testCampaignID, "name": "Bug bash", "reward_asset": "USDC"},
		"repository_full_name": "acme/pool", "pr_number": 7, "event": "opened", "head_sha": nil,
		"status": "completed", "score": 46, "eligible": false, "reward": "0", "evaluation_hash": "0xabc",
		"policy_hash": "0xdef", "settled": false, "error": nil, "created_at": "2026-10-07T05:48:01+00:00",
		"started_at": "2026-10-07T05:48:01+00:00", "finished_at": "2026-10-07T05:49:01+00:00",
		"request":         map[string]any{"repository": "acme/pool", "pr_number": 7},
		"runner_instance": "machine-1",
	}
}

func TestListExecutions(t *testing.T) {
	f := &fakePostgREST{execRows: []map[string]any{fakeExecutionRow()}}
	r := newCampaignTestAPI(t, f)

	w := do(r, http.MethodGet, "/api/executions?campaign_id="+testCampaignID+"&repo=acme/pool&pr=7&status=completed&event=opened", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	q := f.requests[len(f.requests)-1]
	for _, want := range []string{"campaign_id=eq." + testCampaignID, "repository_full_name=eq.acme%2Fpool",
		"pr_number=eq.7", "status=eq.completed", "event=eq.opened", "campaign%3Acampaigns%28id%2Cname%2Creward_asset%29"} {
		require.Contains(t, q, want)
	}
	require.NotContains(t, q, "request") // List responses leave out the payload.

	var out struct {
		Data       []executionItem       `json:"data"`
		Pagination struct{ Total int64 } `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	require.Equal(t, "Bug bash", out.Data[0].Campaign.Name)
	require.Equal(t, int64(1), out.Pagination.Total)

	for _, query := range []string{"campaign_id=nope", "repo=x", "pr=0", "status=done", "event=closed", "page_size=500"} {
		require.Equal(t, http.StatusBadRequest, do(r, http.MethodGet, "/api/executions?"+query, "").Code, query)
	}
}

func TestGetExecution(t *testing.T) {
	f := &fakePostgREST{execRows: []map[string]any{fakeExecutionRow()}}
	r := newCampaignTestAPI(t, f)

	w := do(r, http.MethodGet, "/api/executions/22222222-2222-2222-2222-222222222222", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "machine-1", out.Data["runner_instance"])
	require.Equal(t, map[string]any{"repository": "acme/pool", "pr_number": 7.0}, out.Data["request"])
	require.Equal(t, "Bug bash", out.Data["campaign"].(map[string]any)["name"])
	require.Equal(t, 46.0, out.Data["score"])

	require.Equal(t, http.StatusNotFound, do(r, http.MethodGet, "/api/executions/not-a-uuid", "").Code)
	f.execRows = []map[string]any{}
	require.Equal(t, http.StatusNotFound, do(r, http.MethodGet, "/api/executions/22222222-2222-2222-2222-222222222222", "").Code)
}
