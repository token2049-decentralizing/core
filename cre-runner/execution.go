package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/supabase-community/supabase-go"
	"github.com/token2049-decentralizing/core/cre-runner/internal/privy"
	"github.com/token2049-decentralizing/core/cre-runner/internal/solana"
)

const executionsTable = "cre_executions"

// Execution statuses (cre_executions.status).
const (
	statusQueued    = "queued"
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusSkipped   = "skipped" // merged PR already settled by an earlier execution
)

const errInterrupted = "interrupted: runner shutting down"

// evaluationRequest is the workflow's HTTP trigger payload (cre/test-workflow/types.go).
type evaluationRequest struct {
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number"`
	CampaignID string `json:"campaign_id"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha,omitempty"`
	// PR author's Solana wallet, set for "merged" when Privy is configured.
	RecipientWallet string `json:"recipient_wallet,omitempty"`
}

type evaluationResponse struct {
	Score          int             `json:"score"`
	Eligible       bool            `json:"eligible"`
	Reward         string          `json:"reward"`
	EvaluationHash string          `json:"evaluation_hash"`
	PolicyHash     string          `json:"policy_hash"`
	Scorecard      json.RawMessage `json:"scorecard,omitempty"` // Score breakdown, stored as is.
	PayoutTx       string          `json:"payout_tx,omitempty"` // Solana transaction that paid the reward
}

// prTrigger is the evaluation a pull_request webhook asks for.
type prTrigger struct {
	DeliveryID string
	Repository string
	PRNumber   int
	Event      string // "opened" (preview, never pays) | "merged" (payout)
	HeadSHA    string
	// PR author; the payout goes to their Privy wallet.
	AuthorID    int64
	AuthorLogin string
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// prTriggerFrom maps a pull_request webhook to an evaluation, or nil if it needs none.
func prTriggerFrom(ev *webhookEvent) *prTrigger {
	if ev.Event != "pull_request" || ev.Action == nil || ev.RepositoryFullName == nil ||
		!repoFullNameRe.MatchString(*ev.RepositoryFullName) {
		return nil
	}
	var p struct {
		PullRequest *struct {
			Number int  `json:"number"`
			Draft  bool `json:"draft"`
			Merged bool `json:"merged"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
			User struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
			} `json:"user"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil || p.PullRequest == nil || p.PullRequest.Number <= 0 {
		return nil
	}
	pr := p.PullRequest
	t := &prTrigger{DeliveryID: ev.DeliveryID, Repository: *ev.RepositoryFullName, PRNumber: pr.Number,
		AuthorID: pr.User.ID, AuthorLogin: pr.User.Login}
	switch *ev.Action {
	case "opened", "reopened", "synchronize", "ready_for_review":
		if pr.Draft {
			return nil
		}
		t.Event = "opened"
	case "closed":
		if !pr.Merged {
			return nil
		}
		t.Event = "merged"
	default:
		return nil
	}
	// Pins every review and the score to one commit; the workflow fails if the head moved.
	if shaRe.MatchString(pr.Head.SHA) {
		t.HeadSHA = pr.Head.SHA
	}
	return t
}

// executionRow is a new row in public.cre_executions.
type executionRow struct {
	ID                 string          `json:"id"`
	DeliveryID         string          `json:"delivery_id"`
	CampaignID         string          `json:"campaign_id"`
	RepositoryFullName string          `json:"repository_full_name"`
	PRNumber           int             `json:"pr_number"`
	Event              string          `json:"event"`
	HeadSHA            *string         `json:"head_sha"`
	Status             string          `json:"status"`
	Request            json.RawMessage `json:"request"`
	RunnerInstance     string          `json:"runner_instance"`
	AuthorLogin        *string         `json:"author_login"`
	AuthorGitHubID     *int64          `json:"author_github_id"`
}

// contributorWallet is a row in public.contributor_wallets.
type contributorWallet struct {
	GitHubUserID   int64      `json:"github_user_id"`
	GitHubLogin    string     `json:"github_login"`
	PrivyUserID    string     `json:"privy_user_id"`
	SolanaAddress  string     `json:"solana_address"`
	PregeneratedAt *time.Time `json:"pregenerated_at,omitempty"` // only set when the runner created the Privy user
	UpdatedAt      time.Time  `json:"updated_at"`
}

// executionUpdate is a status change; nil fields are left as they are.
type executionUpdate struct {
	Status          string          `json:"status"`
	Error           *string         `json:"error,omitempty"`
	Score           *int            `json:"score,omitempty"`
	Eligible        *bool           `json:"eligible,omitempty"`
	Reward          *string         `json:"reward,omitempty"`
	EvaluationHash  *string         `json:"evaluation_hash,omitempty"`
	PolicyHash      *string         `json:"policy_hash,omitempty"`
	Scorecard       json.RawMessage `json:"scorecard,omitempty"`
	Settled         *bool           `json:"settled,omitempty"`
	RecipientWallet *string         `json:"recipient_wallet,omitempty"`
	PayoutTx        *string         `json:"payout_tx,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type executionStore interface {
	ActiveCampaigns(repo string) ([]campaignRow, error)
	Insert(row *executionRow) error
	Update(id string, u *executionUpdate) error
	// SettledBy returns the execution that settled this PR for the campaign, or "".
	SettledBy(campaignID, repo string, pr int) (string, error)
	// FailUnfinished marks the instance's queued and running executions as failed.
	FailUnfinished(instance, reason string) error
	// SaveContributorWallet upserts the GitHub user's wallet.
	SaveContributorWallet(w *contributorWallet) error
}

type supabaseStore struct {
	db *supabase.Client
}

func (s supabaseStore) ActiveCampaigns(repo string) ([]campaignRow, error) {
	var rows []struct {
		Campaign campaignRow `json:"campaigns"`
	}
	_, err := s.db.From("campaign_repos").
		Select("campaigns!inner(id,reward_asset,max_reward_per_pr,min_score,eligibility,starts_at,ends_at)", "", false).
		Eq("repository_full_name", repo).
		Eq("campaigns.status", "active").
		ExecuteTo(&rows)
	if err != nil {
		return nil, err
	}
	out := make([]campaignRow, len(rows))
	for i, r := range rows {
		out[i] = r.Campaign
	}
	return out, nil
}

func (s supabaseStore) Insert(row *executionRow) error {
	_, _, err := s.db.From(executionsTable).Insert(row, false, "", "minimal", "").Execute()
	return err
}

func (s supabaseStore) Update(id string, u *executionUpdate) error {
	_, _, err := s.db.From(executionsTable).Update(u, "minimal", "").Eq("id", id).Execute()
	return err
}

func (s supabaseStore) SettledBy(campaignID, repo string, pr int) (string, error) {
	var rows []struct {
		ID string `json:"id"`
	}
	_, err := s.db.From(executionsTable).
		Select("id", "", false).
		Eq("campaign_id", campaignID).
		Eq("repository_full_name", repo).
		Eq("pr_number", strconv.Itoa(pr)).
		Is("settled", "true").
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].ID, nil
}

func (s supabaseStore) FailUnfinished(instance, reason string) error {
	now := time.Now()
	_, _, err := s.db.From(executionsTable).
		Update(&executionUpdate{Status: statusFailed, Error: &reason, FinishedAt: &now}, "minimal", "").
		Eq("runner_instance", instance).
		In("status", []string{statusQueued, statusRunning}).
		Execute()
	return err
}

func (s supabaseStore) SaveContributorWallet(w *contributorWallet) error {
	_, _, err := s.db.From("contributor_wallets").
		Insert(w, true, "github_user_id", "minimal", "").
		Execute()
	return err
}

// isUniqueViolation reports Postgres 23505, e.g. a second settled row for one PR.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

type warmer interface {
	Warm(ctx context.Context, repo string, pr int, headSHA string) error
}

// payoutPreparer reads the campaign on Solana and creates the recipient's token account
// (internal/solana).
type payoutPreparer interface {
	Prepare(ctx context.Context, campaignID, recipient string) (*solana.Settings, error)
}

// walletResolver finds or pregenerates a GitHub user's Solana wallet (internal/privy).
type walletResolver interface {
	WalletForGitHub(ctx context.Context, githubID int64, login string) (*privy.Wallet, error)
}

// executor runs CRE workflow executions in the background and records each one in
// cre_executions: queued -> running -> completed | failed | skipped.
type executor struct {
	store    executionStore
	eval     evaluator
	warm     warmer         // Optional: reviewer cache warm-up before each simulation.
	wallets  walletResolver // Optional: payout recipient for merged PRs.
	payouts  payoutPreparer // Optional: pay merged PRs on Solana through the workflow.
	config   func(campaignRow) (*workflowConfig, error)
	instance string        // runner_instance of rows written by this process
	admit    chan struct{} // queued + running; full -> the execution fails as busy
	workers  chan struct{} // running simulations
	log      *slog.Logger

	ctx    context.Context // Canceled when shutdown runs out of time.
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	settling map[string]*sync.Mutex // campaign|repo|pr -> serializes merged runs of one PR
}

func newExecutor(store executionStore, eval evaluator, config func(campaignRow) (*workflowConfig, error),
	instance string, concurrency, queue int, log *slog.Logger) *executor {
	ctx, cancel := context.WithCancel(context.Background())
	return &executor{
		store:    store,
		eval:     eval,
		config:   config,
		instance: instance,
		admit:    make(chan struct{}, concurrency+queue),
		workers:  make(chan struct{}, concurrency),
		log:      log,
		ctx:      ctx,
		cancel:   cancel,
		settling: map[string]*sync.Mutex{},
	}
}

// RecoverInterrupted fails executions a previous run of this instance left unfinished.
func (x *executor) RecoverInterrupted() error {
	return x.store.FailUnfinished(x.instance, "interrupted: runner restarted")
}

// Submit evaluates the trigger once per active campaign of its repository, in the background.
func (x *executor) Submit(t *prTrigger) {
	x.wg.Add(1)
	go func() {
		defer x.wg.Done()
		campaigns, err := x.store.ActiveCampaigns(t.Repository)
		if err != nil {
			x.log.Error("load campaigns failed", "delivery", t.DeliveryID, "repository", t.Repository, "error", err)
			return
		}
		now := time.Now()
		started := 0
		for _, c := range campaigns {
			if !c.runningAt(now) {
				x.log.Info("campaign outside its schedule, no execution", "delivery", t.DeliveryID,
					"campaign", c.ID, "starts_at", c.StartsAt, "ends_at", c.EndsAt)
				continue
			}
			started++
			x.wg.Add(1)
			go func() {
				defer x.wg.Done()
				x.execute(t, c)
			}()
		}
		x.log.Info("pull request evaluation submitted", "delivery", t.DeliveryID, "repository", t.Repository,
			"pr", t.PRNumber, "event", t.Event, "active_campaigns", len(campaigns), "executions", started)
	}()
}

func (x *executor) execute(t *prTrigger, c campaignRow) {
	req := &evaluationRequest{Repository: t.Repository, PRNumber: t.PRNumber, CampaignID: c.ID, Event: t.Event, HeadSHA: t.HeadSHA}
	reqJSON, _ := json.Marshal(req)
	row := &executionRow{
		ID:                 uuid.NewString(),
		DeliveryID:         t.DeliveryID,
		CampaignID:         c.ID,
		RepositoryFullName: t.Repository,
		PRNumber:           t.PRNumber,
		Event:              t.Event,
		Status:             statusQueued,
		Request:            reqJSON,
		RunnerInstance:     x.instance,
	}
	if t.HeadSHA != "" {
		row.HeadSHA = &t.HeadSHA
	}
	if t.AuthorLogin != "" {
		row.AuthorLogin, row.AuthorGitHubID = &t.AuthorLogin, &t.AuthorID
	}
	log := x.log.With("execution_id", row.ID, "delivery", t.DeliveryID, "campaign", c.ID,
		"repository", t.Repository, "pr", t.PRNumber, "event", t.Event)
	if err := x.store.Insert(row); err != nil {
		log.Error("record execution failed", "error", err)
		return
	}
	fail := func(msg string) {
		log.Warn("execution failed", "error", msg)
		x.finish(log, row.ID, &executionUpdate{Status: statusFailed, Error: &msg})
	}

	cfg, err := x.config(c)
	if err != nil {
		fail(err.Error())
		return
	}

	select {
	case x.admit <- struct{}{}:
		defer func() { <-x.admit }()
	default:
		fail("runner busy: execution queue is full")
		return
	}

	// Merged PRs pay out once per (campaign, repo, PR); webhook redeliveries are skipped.
	if req.Event == "merged" {
		defer x.lockSettlement(c.ID + "|" + req.Repository + "|" + strconv.Itoa(req.PRNumber))()
		prior, err := x.store.SettledBy(c.ID, req.Repository, req.PRNumber)
		if err != nil {
			fail("check settlement: " + err.Error())
			return
		}
		if prior != "" {
			msg := "already settled by execution " + prior
			log.Info("execution skipped", "reason", msg)
			x.finish(log, row.ID, &executionUpdate{Status: statusSkipped, Error: &msg})
			return
		}
	}

	select {
	case x.workers <- struct{}{}:
		defer func() { <-x.workers }()
	case <-x.ctx.Done():
		fail(errInterrupted)
		return
	}

	started := time.Now()
	if err := x.store.Update(row.ID, &executionUpdate{Status: statusRunning, StartedAt: &started}); err != nil {
		log.Error("record running status failed", "error", err)
	}
	if req.Event == "merged" && x.wallets != nil {
		addr, err := x.recipient(log, row.ID, t)
		if err != nil {
			if x.ctx.Err() != nil {
				fail(errInterrupted)
			} else {
				fail("recipient wallet: " + err.Error())
			}
			return
		}
		req.RecipientWallet = addr
	}
	if req.Event == "merged" && x.payouts != nil {
		if req.RecipientWallet == "" {
			fail("solana payout: no recipient wallet (set PRIVY_APP_ID/PRIVY_APP_SECRET)")
			return
		}
		// Not settled in the database unless paid on-chain: fail so the PR can be retried
		// (redeliver the webhook) once the campaign is created and funded on Solana.
		settings, err := x.payouts.Prepare(x.ctx, c.ID, req.RecipientWallet)
		if err != nil {
			if x.ctx.Err() != nil {
				fail(errInterrupted)
			} else {
				fail("solana payout: " + err.Error())
			}
			return
		}
		cfg.Solana = settings
	}
	var res *evaluationResponse
	if x.warm != nil {
		err = x.warm.Warm(x.ctx, req.Repository, req.PRNumber, req.HeadSHA)
	}
	if err == nil {
		res, err = x.eval.Evaluate(x.ctx, req, cfg)
	}
	log = log.With("ms", time.Since(started).Milliseconds())
	switch {
	case x.ctx.Err() != nil:
		fail(errInterrupted)
		return
	case errors.Is(err, context.DeadlineExceeded):
		fail("timed out: " + err.Error())
		return
	case err != nil:
		fail(err.Error())
		return
	}

	settled := req.Event == "merged" && res.Eligible && res.Reward != "0"
	u := &executionUpdate{
		Status:         statusCompleted,
		Score:          &res.Score,
		Eligible:       &res.Eligible,
		Reward:         &res.Reward,
		EvaluationHash: &res.EvaluationHash,
		PolicyHash:     &res.PolicyHash,
		Settled:        &settled,
	}
	if res.PayoutTx != "" {
		u.PayoutTx = &res.PayoutTx
	}
	if len(res.Scorecard) > 0 && string(res.Scorecard) != "null" {
		u.Scorecard = res.Scorecard
	}
	err = x.finish(log, row.ID, u)
	if settled && isUniqueViolation(err) {
		// Another runner instance settled this PR first: keep the result, never pay twice.
		settled = false
		msg := "already settled by another execution"
		u.Error = &msg
		err = x.finish(log, row.ID, u)
	}
	if err == nil {
		log.Info("execution completed", "score", res.Score, "eligible", res.Eligible, "settled", settled, "payout_tx", res.PayoutTx)
	}
}

// recipient resolves the PR author's Solana wallet, pregenerating one from their GitHub
// account if they never signed in, and records it before the workflow runs.
func (x *executor) recipient(log *slog.Logger, id string, t *prTrigger) (string, error) {
	if t.AuthorID <= 0 || t.AuthorLogin == "" {
		return "", errors.New("webhook has no pull request author")
	}
	w, err := x.wallets.WalletForGitHub(x.ctx, t.AuthorID, t.AuthorLogin)
	if err != nil {
		return "", err
	}
	now := time.Now()
	cw := &contributorWallet{GitHubUserID: t.AuthorID, GitHubLogin: t.AuthorLogin, PrivyUserID: w.PrivyUserID,
		SolanaAddress: w.Address, UpdatedAt: now}
	if w.Pregenerated {
		cw.PregeneratedAt = &now
	}
	if err := x.store.SaveContributorWallet(cw); err != nil {
		log.Error("record contributor wallet failed", "error", err)
	}
	if err := x.store.Update(id, &executionUpdate{Status: statusRunning, RecipientWallet: &w.Address}); err != nil {
		log.Error("record recipient wallet failed", "error", err)
	}
	log.Info("payout recipient resolved", "author", t.AuthorLogin, "wallet", w.Address, "pregenerated", w.Pregenerated)
	return w.Address, nil
}

// finish records a terminal status.
func (x *executor) finish(log *slog.Logger, id string, u *executionUpdate) error {
	now := time.Now()
	u.FinishedAt = &now
	err := x.store.Update(id, u)
	if err != nil && !isUniqueViolation(err) {
		log.Error("record execution result failed", "status", u.Status, "error", err)
	}
	return err
}

// lockSettlement serializes merged executions of one PR; call the returned func to unlock.
func (x *executor) lockSettlement(key string) func() {
	x.mu.Lock()
	m, ok := x.settling[key]
	if !ok {
		m = &sync.Mutex{}
		x.settling[key] = m
	}
	x.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Shutdown waits for executions until ctx ends, then interrupts the rest (recorded as failed).
func (x *executor) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		x.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	x.log.Warn("shutdown timeout, interrupting executions")
	x.cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}
