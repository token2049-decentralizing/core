package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRecord struct {
	row      executionRow
	statuses []string
	err      string
	settled  bool
	score    *int
}

type fakeStore struct {
	mu        sync.Mutex
	campaigns []campaignRow
	records   map[string]*fakeRecord
	order     []string
}

func newFakeStore(campaigns ...campaignRow) *fakeStore {
	return &fakeStore{campaigns: campaigns, records: map[string]*fakeRecord{}}
}

func (f *fakeStore) ActiveCampaigns(string) ([]campaignRow, error) { return f.campaigns, nil }

func (f *fakeStore) Insert(row *executionRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[row.ID] = &fakeRecord{row: *row, statuses: []string{row.Status}}
	f.order = append(f.order, row.ID)
	return nil
}

func (f *fakeStore) Update(id string, u *executionUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.records[id]
	if u.Settled != nil && *u.Settled {
		for oid, o := range f.records {
			if oid != id && o.settled && o.row.CampaignID == r.row.CampaignID &&
				o.row.RepositoryFullName == r.row.RepositoryFullName && o.row.PRNumber == r.row.PRNumber {
				return errors.New(`(23505) duplicate key value violates unique constraint "cre_executions_settled_uniq"`)
			}
		}
	}
	r.statuses = append(r.statuses, u.Status)
	if u.Error != nil {
		r.err = *u.Error
	}
	if u.Settled != nil {
		r.settled = *u.Settled
	}
	if u.Score != nil {
		r.score = u.Score
	}
	return nil
}

func (f *fakeStore) SettledBy(campaignID, repo string, pr int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, r := range f.records {
		if r.settled && r.row.CampaignID == campaignID && r.row.RepositoryFullName == repo && r.row.PRNumber == pr {
			return id, nil
		}
	}
	return "", nil
}

func (f *fakeStore) FailUnfinished(instance, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.records {
		last := r.statuses[len(r.statuses)-1]
		if r.row.RunnerInstance == instance && (last == statusQueued || last == statusRunning) {
			r.statuses = append(r.statuses, statusFailed)
			r.err = reason
		}
	}
	return nil
}

// list returns the records in insertion order.
func (f *fakeStore) list() []*fakeRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*fakeRecord, len(f.order))
	for i, id := range f.order {
		out[i] = f.records[id]
	}
	return out
}

type fakeEval func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error)

func (f fakeEval) Evaluate(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
	return f(ctx, req, cfg)
}

type fakeWarm func(ctx context.Context, repo string, pr int, head string) error

func (f fakeWarm) Warm(ctx context.Context, repo string, pr int, head string) error {
	return f(ctx, repo, pr, head)
}

var usdcCampaign = campaignRow{ID: "11111111-1111-1111-1111-111111111111", RewardAsset: "USDC", MaxRewardPerPR: "500.000000"}

func newTestExecutor(store executionStore, eval fakeEval, concurrency, queue int) *executor {
	config := func(c campaignRow) (*workflowConfig, error) {
		return buildWorkflowConfig(c, "https://api.github.com", "")
	}
	return newExecutor(store, eval, config, "machine-1", concurrency, queue, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// wait blocks until every submitted execution has finished.
func wait(x *executor) { x.Shutdown(context.Background()) }

func okResult(context.Context, *evaluationRequest, *workflowConfig) (*evaluationResponse, error) {
	return &evaluationResponse{Score: 30, Reward: "0", EvaluationHash: "0xabc", PolicyHash: "0xdef"}, nil
}

func eligibleResult(context.Context, *evaluationRequest, *workflowConfig) (*evaluationResponse, error) {
	return &evaluationResponse{Score: 90, Eligible: true, Reward: "450000000", EvaluationHash: "0x1", PolicyHash: "0xdef"}, nil
}

func prEvent(action, payload string) *webhookEvent {
	repo := "acme/pool"
	return &webhookEvent{DeliveryID: "d1", Event: "pull_request", Action: &action, RepositoryFullName: &repo, Payload: []byte(payload)}
}

func TestPRTriggerFrom(t *testing.T) {
	sha := strings.Repeat("a", 40)
	head := `"head":{"sha":"` + sha + `"}`

	tr := prTriggerFrom(prEvent("opened", `{"pull_request":{"number":7,`+head+`}}`))
	require.Equal(t, &prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "opened", HeadSHA: sha}, tr)
	require.Equal(t, "opened", prTriggerFrom(prEvent("synchronize", `{"pull_request":{"number":7,`+head+`}}`)).Event)
	require.Equal(t, "merged", prTriggerFrom(prEvent("closed", `{"pull_request":{"number":7,"merged":true,`+head+`}}`)).Event)

	for name, ev := range map[string]*webhookEvent{
		"closed unmerged": prEvent("closed", `{"pull_request":{"number":7,"merged":false}}`),
		"draft":           prEvent("opened", `{"pull_request":{"number":7,"draft":true}}`),
		"labeled":         prEvent("labeled", `{"pull_request":{"number":7}}`),
		"no PR":           prEvent("opened", `{}`),
		"other event":     {Event: "issues", Action: new(string), Payload: []byte(`{}`)},
	} {
		require.Nil(t, prTriggerFrom(ev), name)
	}

	// A malformed head SHA is dropped instead of failing the workflow's validation.
	require.Empty(t, prTriggerFrom(prEvent("opened", `{"pull_request":{"number":7,"head":{"sha":"xyz"}}}`)).HeadSHA)
}

func TestBuildWorkflowConfig(t *testing.T) {
	minScore := 70
	c := usdcCampaign
	c.MinScore = &minScore
	c.Eligibility = map[string]any{"merged": true, "ci_passed": true, "linked_issue": false, "duplicate": "n/a"}
	cfg, err := buildWorkflowConfig(c, "https://api.github.com", "http://127.0.0.1:8080")
	require.NoError(t, err)

	p := cfg.Campaign
	require.Equal(t, usdcCampaign.ID, p.ID)
	require.Equal(t, 6, p.TokenDecimals)
	require.Equal(t, "500", p.Reward.Max)
	require.Equal(t, "score_based", p.Reward.Model)
	require.True(t, p.Eligibility.RequireMerged)
	require.True(t, p.Eligibility.RequireCIPassed)
	require.False(t, p.Eligibility.RequireLinkedIssue)
	require.Equal(t, 70, p.Eligibility.MinScore)
	require.Equal(t, 10000, p.Weights.Evidence+p.Weights.CodeReviewer+p.Weights.LLM)
	require.Equal(t, "http://127.0.0.1:8080/review/code", cfg.Reviewers.CodeReviewer.URL)
	require.Equal(t, "http://127.0.0.1:8080/review/issue", cfg.Reviewers.LLM.URL)

	sol := usdcCampaign
	sol.RewardAsset, sol.MaxRewardPerPR = "SOL", "1.250000"
	cfg, err = buildWorkflowConfig(sol, "https://api.github.com", "")
	require.NoError(t, err)
	require.Equal(t, 9, cfg.Campaign.TokenDecimals)
	require.Equal(t, "1.25", cfg.Campaign.Reward.Max)
	require.Empty(t, cfg.Reviewers.LLM.URL) // No reviewer: stub scores.

	sol.RewardAsset = "ETH"
	_, err = buildWorkflowConfig(sol, "https://api.github.com", "")
	require.ErrorContains(t, err, "unsupported reward asset")
}

// The workflow validates the same file (cre/test-workflow: TestRunnerGeneratedConfig),
// keeping workflowConfig in sync with the workflow's Config across the two modules.
func TestWorkflowConfigGolden(t *testing.T) {
	minScore := 70
	c := usdcCampaign
	c.MinScore = &minScore
	c.Eligibility = map[string]any{"merged": true, "ci_passed": true, "linked_issue": true}
	cfg, err := buildWorkflowConfig(c, "https://api.github.com", "http://127.0.0.1:8080")
	require.NoError(t, err)
	got, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)

	golden := filepath.Join("cre", "test-workflow", "testdata", "runner-config.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(golden, append(got, '\n'), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got), "run with UPDATE_GOLDEN=1 and check the workflow tests")
}

func TestExecutionRecordsLifecycle(t *testing.T) {
	future := time.Now().Add(time.Hour)
	notStarted := campaignRow{ID: "22222222-2222-2222-2222-222222222222", RewardAsset: "USDC", MaxRewardPerPR: "1", StartsAt: &future}
	store := newFakeStore(usdcCampaign, notStarted)

	var got *evaluationRequest
	x := newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		got = req
		assert.Equal(t, req.CampaignID, cfg.Campaign.ID)
		return okResult(ctx, req, cfg)
	}, 1, 1)
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "opened"})
	wait(x)

	recs := store.list()
	require.Len(t, recs, 1) // The campaign that has not started is skipped.
	r := recs[0]
	require.Equal(t, []string{statusQueued, statusRunning, statusCompleted}, r.statuses)
	require.Equal(t, usdcCampaign.ID, r.row.CampaignID)
	require.Equal(t, "d1", r.row.DeliveryID)
	require.Equal(t, "machine-1", r.row.RunnerInstance)
	require.JSONEq(t, `{"repository":"acme/pool","pr_number":7,"campaign_id":"`+usdcCampaign.ID+`","event":"opened"}`, string(r.row.Request))
	require.Equal(t, 30, *r.score)
	require.False(t, r.settled)
	require.Equal(t, usdcCampaign.ID, got.CampaignID)
}

func TestExecutionFailureIsRecorded(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	x := newTestExecutor(store, func(context.Context, *evaluationRequest, *workflowConfig) (*evaluationResponse, error) {
		return nil, &evalError{msg: "GitHub /repos/acme/pool/pulls/7 -> HTTP 404"}
	}, 1, 1)
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "opened"})
	wait(x)

	r := store.list()[0]
	require.Equal(t, []string{statusQueued, statusRunning, statusFailed}, r.statuses)
	require.Contains(t, r.err, "HTTP 404")
}

func TestWarmFailureSkipsSimulation(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	var evals atomic.Int32
	x := newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		evals.Add(1)
		return okResult(ctx, req, cfg)
	}, 1, 1)
	sha := strings.Repeat("b", 40)
	x.warm = fakeWarm(func(_ context.Context, repo string, pr int, head string) error {
		assert.Equal(t, "acme/pool", repo)
		assert.Equal(t, sha, head)
		return errors.New("reviewer code: llm -> HTTP 500")
	})
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "opened", HeadSHA: sha})
	wait(x)

	r := store.list()[0]
	require.Equal(t, statusFailed, r.statuses[len(r.statuses)-1])
	require.Contains(t, r.err, "llm -> HTTP 500")
	require.Zero(t, evals.Load())
}

func TestMergedIsSettledOnce(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	var evals atomic.Int32
	x := newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		evals.Add(1)
		time.Sleep(20 * time.Millisecond)
		return eligibleResult(ctx, req, cfg)
	}, 4, 4)
	merged := &prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "merged"}

	// Concurrent duplicates (redeliveries) wait for the first; only one settles.
	for range 3 {
		x.Submit(merged)
	}
	wait(x)
	require.Equal(t, int32(1), evals.Load())
	var settled, skipped int
	for _, r := range store.list() {
		switch r.statuses[len(r.statuses)-1] {
		case statusCompleted:
			require.True(t, r.settled)
			settled++
		case statusSkipped:
			require.Contains(t, r.err, "already settled by execution")
			skipped++
		}
	}
	require.Equal(t, 1, settled)
	require.Equal(t, 2, skipped)

	// "opened" is a preview and never deduplicated.
	x = newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		evals.Add(1)
		return eligibleResult(ctx, req, cfg)
	}, 1, 1)
	x.Submit(&prTrigger{DeliveryID: "d2", Repository: "acme/pool", PRNumber: 7, Event: "opened"})
	wait(x)
	require.Equal(t, int32(2), evals.Load())
	require.False(t, store.list()[3].settled)
}

func TestSettlementConflictKeepsResultWithoutPaying(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	// A settled row written by another runner instance.
	other := &executionRow{ID: "other", CampaignID: usdcCampaign.ID, RepositoryFullName: "acme/pool", PRNumber: 7, Status: statusCompleted}
	require.NoError(t, store.Insert(other))
	x := newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		// Lands between this instance's settlement check and its result write.
		store.mu.Lock()
		store.records["other"].settled = true
		store.mu.Unlock()
		return eligibleResult(ctx, req, cfg)
	}, 1, 1)
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 7, Event: "merged"})
	wait(x)

	r := store.list()[1]
	require.Equal(t, statusCompleted, r.statuses[len(r.statuses)-1])
	require.False(t, r.settled)
	require.Equal(t, 90, *r.score)
	require.Equal(t, "already settled by another execution", r.err)
}

func TestBusyExecutionFails(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	release, started := make(chan struct{}), make(chan struct{}, 1)
	x := newTestExecutor(store, func(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
		started <- struct{}{}
		<-release
		return okResult(ctx, req, cfg)
	}, 1, 0)
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 1, Event: "opened"})
	<-started
	x.Submit(&prTrigger{DeliveryID: "d2", Repository: "acme/pool", PRNumber: 2, Event: "opened"})
	require.Eventually(t, func() bool {
		recs := store.list()
		return len(recs) == 2 && recs[1].err != ""
	}, time.Second, 5*time.Millisecond)
	close(release)
	wait(x)

	recs := store.list()
	require.Equal(t, statusCompleted, recs[0].statuses[len(recs[0].statuses)-1])
	require.Equal(t, []string{statusQueued, statusFailed}, recs[1].statuses)
	require.Contains(t, recs[1].err, "busy")
}

func TestShutdownInterruptsAndRestartRecovers(t *testing.T) {
	store := newFakeStore(usdcCampaign)
	started := make(chan struct{}, 1)
	x := newTestExecutor(store, func(ctx context.Context, _ *evaluationRequest, _ *workflowConfig) (*evaluationResponse, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, 1, 4)
	x.Submit(&prTrigger{DeliveryID: "d1", Repository: "acme/pool", PRNumber: 1, Event: "opened"})
	<-started
	x.Submit(&prTrigger{DeliveryID: "d2", Repository: "acme/pool", PRNumber: 2, Event: "opened"}) // Queued.
	require.Eventually(t, func() bool { return len(store.list()) == 2 }, time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	x.Shutdown(ctx)
	for _, r := range store.list() {
		require.Equal(t, statusFailed, r.statuses[len(r.statuses)-1])
		require.Equal(t, errInterrupted, r.err)
	}

	// A hard kill leaves rows unfinished; the next start of the same machine fails them.
	require.NoError(t, store.Insert(&executionRow{ID: "stale", Status: statusRunning, RunnerInstance: "machine-1"}))
	require.NoError(t, store.Insert(&executionRow{ID: "elsewhere", Status: statusRunning, RunnerInstance: "machine-2"}))
	require.NoError(t, newTestExecutor(store, okResult, 1, 1).RecoverInterrupted())
	require.Equal(t, statusFailed, store.records["stale"].statuses[1])
	require.Len(t, store.records["elsewhere"].statuses, 1)
}
