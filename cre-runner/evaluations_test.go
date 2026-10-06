package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	testCampaign = "6f1c2a9e-3b7d-4c1e-9a55-0d2f8b7e4c11"
	testSHA      = "1111111111111111111111111111111111111111"
)

// fakeStore is an in-memory evalStore.
type fakeStore struct {
	mu        sync.Mutex
	campaigns map[string][]string // repo -> active campaign ids
	rows      map[int64]map[string]any
	next      int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{campaigns: map[string][]string{}, rows: map[int64]map[string]any{}}
}

func (s *fakeStore) activeCampaignsForRepo(repo string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.campaigns[repo], nil
}

func (s *fakeStore) createEvaluation(r evaluationRow) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Trigger == "webhook" { // Same rule as evaluations_webhook_dedup_idx.
		for _, row := range s.rows {
			if row["trigger"] == "webhook" && row["campaign_id"] == r.CampaignID && row["repository_full_name"] == r.RepositoryFullName &&
				row["pr_number"] == r.PRNumber && row["head_sha"] == deref(r.HeadSHA) && row["event"] == r.Event {
				return 0, false, nil
			}
		}
	}
	s.next++
	s.rows[s.next] = map[string]any{
		"id": s.next, "campaign_id": r.CampaignID, "repository_full_name": r.RepositoryFullName, "pr_number": r.PRNumber,
		"head_sha": deref(r.HeadSHA), "event": r.Event, "trigger": r.Trigger, "status": r.Status,
	}
	return s.next, true, nil
}

func (s *fakeStore) updateEvaluation(id int64, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range fields {
		s.rows[id][k] = v
	}
	return nil
}

func (s *fakeStore) failInterrupted() error { return nil }

func (s *fakeStore) listEvaluations(f evaluationListFilter) ([]map[string]json.RawMessage, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]json.RawMessage
	for _, row := range s.rows {
		out = append(out, toRaw(row))
	}
	return out, int64(len(out)), nil
}

func (s *fakeStore) getEvaluation(id int64) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.rows[id]; ok {
		return toRaw(row), nil
	}
	return nil, nil
}

func (s *fakeStore) row(id int64) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[id]
}

func toRaw(row map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range row {
		out[k], _ = json.Marshal(v)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type fakeRunner struct {
	calls atomic.Int32
	fn    func(req evaluateRequest) (*evaluateResult, error)
}

func (f *fakeRunner) Evaluate(_ context.Context, req evaluateRequest) (*evaluateResult, error) {
	f.calls.Add(1)
	return f.fn(req)
}

func prPayload(action string, merged, draft bool) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"action":       action,
		"pull_request": map[string]any{"number": 7, "merged": merged, "draft": draft, "head": map[string]any{"sha": testSHA}},
		"repository":   map[string]any{"full_name": "acme/pool"},
	})
	return b
}

func TestPREventFrom(t *testing.T) {
	cases := []struct {
		event, action string
		merged, draft bool
		want          string // "" = not evaluated
	}{
		{"pull_request", "opened", false, false, "opened"},
		{"pull_request", "synchronize", false, false, "opened"},
		{"pull_request", "reopened", false, false, "opened"},
		{"pull_request", "ready_for_review", false, false, "opened"},
		{"pull_request", "closed", true, false, "merged"},
		{"pull_request", "closed", false, false, ""},
		{"pull_request", "opened", false, true, ""},
		{"pull_request", "labeled", false, false, ""},
		{"issues", "opened", false, false, ""},
	}
	for _, c := range cases {
		ev, ok := prEventFrom(c.event, prPayload(c.action, c.merged, c.draft))
		if got := map[bool]string{true: ev.Event, false: ""}[ok]; got != c.want {
			t.Errorf("%s/%s merged=%t draft=%t: got %q, want %q", c.event, c.action, c.merged, c.draft, got, c.want)
		}
		if ok && (ev.Repo != "acme/pool" || ev.Number != 7 || ev.HeadSHA != testSHA) {
			t.Errorf("bad mapping: %+v", ev)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestWebhookQueuesOncePerCampaign(t *testing.T) {
	store := newFakeStore()
	store.campaigns["acme/pool"] = []string{testCampaign, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}
	runner := &fakeRunner{fn: func(req evaluateRequest) (*evaluateResult, error) {
		if req.HeadSHA != testSHA || req.Event != "merged" {
			return nil, fmt.Errorf("bad request %+v", req)
		}
		return &evaluateResult{Score: 91, Eligible: true, Reward: "455000000", EvaluationHash: "0xe", PolicyHash: "0xp"}, nil
	}}
	e := &evaluations{store: store, runner: runner, sem: make(chan struct{}, 2), timeout: time.Second}

	ev := &webhookEvent{Event: "pull_request", DeliveryID: "d1", Payload: prPayload("closed", true, false)}
	e.fromWebhook(ev)
	e.fromWebhook(ev) // GitHub redelivery: deduplicated.
	e.wg.Wait()

	if n := runner.calls.Load(); n != 2 {
		t.Fatalf("runner calls = %d, want 2 (one per campaign)", n)
	}
	for id := int64(1); id <= 2; id++ {
		row := store.row(id)
		if row["status"] != "done" || row["score"] != 91 || row["reward"] != "455000000" || row["trigger"] != "webhook" {
			t.Errorf("row %d = %v", id, row)
		}
	}

	// Repo without an active campaign: nothing queued.
	e.fromWebhook(&webhookEvent{Event: "pull_request", Payload: json.RawMessage(strings.Replace(string(prPayload("opened", false, false)), "acme/pool", "acme/none", 1))})
	e.wg.Wait()
	if n := runner.calls.Load(); n != 2 {
		t.Fatalf("runner calls = %d after unrelated repo", n)
	}
}

func TestFailedRunIsRecorded(t *testing.T) {
	store := newFakeStore()
	runner := &fakeRunner{fn: func(evaluateRequest) (*evaluateResult, error) {
		return nil, errors.New("runner -> HTTP 502: PR head moved")
	}}
	e := &evaluations{store: store, runner: runner, sem: make(chan struct{}, 1), timeout: time.Second}
	id, created, err := e.enqueue(evaluationRow{CampaignID: testCampaign, RepositoryFullName: "acme/pool", PRNumber: 7, Event: "opened", Trigger: "manual"})
	if err != nil || !created {
		t.Fatal(err)
	}
	e.wg.Wait()
	if row := store.row(id); row["status"] != "failed" || !strings.Contains(fmt.Sprint(row["error"]), "head moved") {
		t.Fatalf("row = %v", row)
	}
}

func TestEvaluatorClient(t *testing.T) {
	const secret = "runner-shared-secret-123"
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		if r.URL.Path != "/evaluate" || r.Header.Get("X-ContribOracle-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req evaluateRequest
		_ = json.Unmarshal(body, &req)
		switch {
		case req.PRNumber == 404:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":"GitHub -> HTTP 404"}`)
		case attempts.Add(1) == 1: // Busy once, then succeed.
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"busy, retry later"}`)
		default:
			w.Header().Set("X-ContribOracle-Replay", "true")
			_, _ = io.WriteString(w, `{"score":90,"eligible":true,"reward":"1","evaluation_hash":"0xe","policy_hash":"0xp"}`)
		}
	}))
	defer srv.Close()

	var slept []time.Duration
	c := newEvaluatorClient(srv.URL+"/", secret)
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	res, err := c.Evaluate(context.Background(), evaluateRequest{Repository: "acme/pool", PRNumber: 7, CampaignID: testCampaign, Event: "merged"})
	if err != nil || res.Score != 90 || !res.Replayed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Fatalf("slept %v, want one 3s Retry-After wait", slept)
	}

	_, err = c.Evaluate(context.Background(), evaluateRequest{Repository: "acme/pool", PRNumber: 404, CampaignID: testCampaign, Event: "opened"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}

func apiServer(evals *evaluations, store evalStore) *httptest.Server {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerEvaluationsAPI(r, evals, store, "admin-key")
	return httptest.NewServer(r)
}

func call(t *testing.T, method, url, auth, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestEvaluationsAPI(t *testing.T) {
	store := newFakeStore()
	runner := &fakeRunner{fn: func(evaluateRequest) (*evaluateResult, error) {
		return &evaluateResult{Score: 40, Reward: "0", EvaluationHash: "0xe", PolicyHash: "0xp"}, nil
	}}
	e := &evaluations{store: store, runner: runner, sem: make(chan struct{}, 1), timeout: time.Second}
	srv := apiServer(e, store)
	defer srv.Close()

	body := `{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":7}`
	if status, _ := call(t, "POST", srv.URL+"/api/evaluations", "", body); status != http.StatusUnauthorized {
		t.Fatalf("no key: %d", status)
	}
	if status, _ := call(t, "POST", srv.URL+"/api/evaluations", "wrong", body); status != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", status)
	}
	for _, bad := range []string{
		`{"campaign_id":"nope","repository":"acme/pool","pr_number":7}`,
		`{"campaign_id":"` + testCampaign + `","repository":"pool","pr_number":7}`,
		`{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":0}`,
		`{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":7,"event":"closed"}`,
		`{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":7,"head_sha":"abc"}`,
		`{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":7,"extra":1}`,
	} {
		if status, _ := call(t, "POST", srv.URL+"/api/evaluations", "admin-key", bad); status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, status)
		}
	}

	status, out := call(t, "POST", srv.URL+"/api/evaluations", "admin-key", body)
	if status != http.StatusAccepted {
		t.Fatalf("create: %d %v", status, out)
	}
	data := out["data"].(map[string]any)
	if data["event"] != "opened" || data["trigger"] != "manual" {
		t.Fatalf("created = %v", data)
	}
	id := fmt.Sprint(data["id"])
	waitFor(t, func() bool { return store.row(1)["status"] == "done" })

	status, out = call(t, "GET", srv.URL+"/api/evaluations/"+id, "", "")
	if status != 200 || out["data"].(map[string]any)["score"] != 40.0 {
		t.Fatalf("get: %d %v", status, out)
	}
	if status, _ := call(t, "GET", srv.URL+"/api/evaluations/999", "", ""); status != http.StatusNotFound {
		t.Fatalf("missing: %d", status)
	}
	status, out = call(t, "GET", srv.URL+"/api/evaluations?repo=acme/pool&pr_number=7", "", "")
	if status != 200 || out["pagination"].(map[string]any)["total"] != 1.0 {
		t.Fatalf("list: %d %v", status, out)
	}
	if status, _ := call(t, "GET", srv.URL+"/api/evaluations?status=weird", "", ""); status != http.StatusBadRequest {
		t.Fatalf("bad status filter: %d", status)
	}
}

func TestManualTriggerDisabledWithoutRunner(t *testing.T) {
	srv := apiServer(nil, newFakeStore())
	defer srv.Close()
	body := `{"campaign_id":"` + testCampaign + `","repository":"acme/pool","pr_number":7}`
	if status, _ := call(t, "POST", srv.URL+"/api/evaluations", "admin-key", body); status != http.StatusServiceUnavailable {
		t.Fatalf("disabled: %d", status)
	}
}
