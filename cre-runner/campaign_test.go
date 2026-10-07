package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/supabase-community/supabase-go"
)

const testCampaignID = "11111111-1111-1111-1111-111111111111"

// fakePostgREST records the requests the API makes and serves one stored campaign.
type fakePostgREST struct {
	mu         sync.Mutex
	campaign   map[string]any // nil = not found
	repos      []string
	executions int
	execRows   []map[string]any // served by GET cre_executions
	requests   []string         // "METHOD table?query"
	bodies     map[string]string
}

func (f *fakePostgREST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
	key := r.Method + " " + table
	f.requests = append(f.requests, key+"?"+r.URL.RawQuery)
	body, _ := io.ReadAll(r.Body)
	f.bodies[key] = string(body)
	w.Header().Set("Content-Type", "application/json")

	switch key {
	case "GET campaigns":
		if f.campaign == nil {
			_, _ = io.WriteString(w, "[]")
			return
		}
		row := map[string]any{}
		for k, v := range f.campaign {
			row[k] = v
		}
		links := []map[string]string{}
		for _, repo := range f.repos {
			links = append(links, map[string]string{"repository_full_name": repo})
		}
		row["campaign_repos"] = links
		_ = json.NewEncoder(w).Encode([]any{row})
	case "PATCH campaigns":
		var u map[string]any
		_ = json.Unmarshal(body, &u)
		for k, v := range u {
			f.campaign[k] = v
		}
		w.WriteHeader(http.StatusNoContent)
	case "DELETE campaign_repos":
		w.WriteHeader(http.StatusNoContent)
	case "POST campaign_repos":
		var rows []campaignRepoInsert
		_ = json.Unmarshal(body, &rows)
		for _, row := range rows {
			f.repos = append(f.repos, row.RepositoryFullName)
		}
		w.WriteHeader(http.StatusCreated)
	case "HEAD cre_executions", "GET cre_executions":
		w.Header().Set("Content-Range", "*/"+itoa(max(f.executions, len(f.execRows))))
		rows := f.execRows
		if rows == nil {
			rows = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(rows)
	case "DELETE campaigns":
		f.campaign = nil
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+key, http.StatusTeapot)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// handlerTransport serves requests in-process, so tests need no listening port.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	return w.Result(), nil
}

func newCampaignTestAPI(t *testing.T, f *fakePostgREST) *gin.Engine {
	gin.SetMode(gin.TestMode)
	f.bodies = map[string]string{}
	// postgrest-go sends through http.DefaultTransport and offers no other hook.
	orig := http.DefaultTransport
	http.DefaultTransport = handlerTransport{f}
	t.Cleanup(func() { http.DefaultTransport = orig })
	db, err := supabase.NewClient("http://supabase.test", "test-key", nil)
	require.NoError(t, err)
	r := gin.New()
	registerAPI(r, db)
	return r
}

func storedCampaign() map[string]any {
	return map[string]any{
		"id": testCampaignID, "name": "Bug bash", "description": "old", "sponsor": nil,
		"reward_asset": "USDC", "budget": 1000.0, "max_reward_per_pr": 100.0, "min_score": 70,
		"eligibility": map[string]any{"merged": true}, "scoring": map[string]any{},
		"status": "draft", "treasury_address": nil, "starts_at": nil, "ends_at": nil,
		"created_at": "2026-10-07T00:00:00+00:00",
	}
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader([]byte(body))))
	return w
}

func TestUpdateCampaign(t *testing.T) {
	f := &fakePostgREST{campaign: storedCampaign(), repos: []string{"acme/pool", "acme/old"}}
	r := newCampaignTestAPI(t, f)

	w := do(r, http.MethodPatch, "/api/campaigns/"+testCampaignID,
		`{"status":"active","description":null,"repos":["acme/pool","acme/new"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Unchanged fields are written back as stored; null clears; status may now be active.
	var written map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.bodies["PATCH campaigns"]), &written))
	require.Equal(t, "active", written["status"])
	require.Equal(t, "Bug bash", written["name"])
	require.Nil(t, written["description"])
	require.Contains(t, written, "description") // Sent as null, not omitted.
	require.Equal(t, 70.0, written["min_score"])
	require.Equal(t, map[string]any{"merged": true}, written["eligibility"])

	// Repos are diffed: one removed, one added.
	require.Contains(t, f.requests, "DELETE campaign_repos?campaign_id=eq."+testCampaignID+"&repository_full_name=in.%28acme%2Fold%29")
	require.JSONEq(t, `[{"campaign_id":"`+testCampaignID+`","repository_full_name":"acme/new"}]`, f.bodies["POST campaign_repos"])

	var out struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "active", out.Data["status"])
}

func TestUpdateCampaignKeepsReposUnlessSent(t *testing.T) {
	f := &fakePostgREST{campaign: storedCampaign(), repos: []string{"acme/pool"}}
	r := newCampaignTestAPI(t, f)

	w := do(r, http.MethodPatch, "/api/campaigns/"+testCampaignID, `{"status":"paused"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	for _, req := range f.requests {
		require.NotContains(t, req, "campaign_repos?", "repos must not be touched")
	}
}

func TestUpdateCampaignValidation(t *testing.T) {
	f := &fakePostgREST{campaign: storedCampaign()}
	r := newCampaignTestAPI(t, f)

	for body, msg := range map[string]string{
		`{"status":"archived"}`:        "status must be one of",
		`{"max_reward_per_pr":"5000"}`: "must not exceed budget",
		`{"reward":"x"}`:               "unknown field",
		`[]`:                           "JSON object",
		`{"repos":["not a repo"]}`:     "invalid repo",
		`{"name":"  "}`:                "name is required",
	} {
		w := do(r, http.MethodPatch, "/api/campaigns/"+testCampaignID, body)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.Contains(t, w.Body.String(), msg, body)
	}
	require.NotContains(t, f.bodies, "PATCH campaigns")

	require.Equal(t, http.StatusNotFound, do(r, http.MethodPatch, "/api/campaigns/not-a-uuid", `{}`).Code)
	f.campaign = nil
	require.Equal(t, http.StatusNotFound, do(r, http.MethodPatch, "/api/campaigns/"+testCampaignID, `{}`).Code)
}

func TestDeleteCampaign(t *testing.T) {
	f := &fakePostgREST{campaign: storedCampaign(), executions: 3}
	r := newCampaignTestAPI(t, f)

	w := do(r, http.MethodDelete, "/api/campaigns/"+testCampaignID, "")
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "3 CRE executions")
	require.NotNil(t, f.campaign)

	f.executions = 0
	w = do(r, http.MethodDelete, "/api/campaigns/"+testCampaignID, "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Nil(t, f.campaign)

	require.Equal(t, http.StatusNotFound, do(r, http.MethodDelete, "/api/campaigns/"+testCampaignID, "").Code)
}
