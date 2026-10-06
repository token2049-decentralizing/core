package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// evaluations queues PR evaluations and runs them in the background against the CRE runner.
type evaluations struct {
	store  evalStore
	runner interface {
		Evaluate(ctx context.Context, req evaluateRequest) (*evaluateResult, error)
	}
	sem     chan struct{} // Concurrent runner calls.
	timeout time.Duration
	wg      sync.WaitGroup
}

func newEvaluations(store evalStore, runner *evaluatorClient, concurrency int) *evaluations {
	return &evaluations{store: store, runner: runner, sem: make(chan struct{}, concurrency), timeout: 10 * time.Minute}
}

// enqueue stores a pending row and starts the run. created=false: duplicate webhook, nothing to do.
func (e *evaluations) enqueue(row evaluationRow) (int64, bool, error) {
	row.Status = "pending"
	id, created, err := e.store.createEvaluation(row)
	if err != nil || !created {
		return id, created, err
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(id, row)
	}()
	return id, true, nil
}

func (e *evaluations) run(id int64, row evaluationRow) {
	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	if err := e.store.updateEvaluation(id, map[string]any{"status": "running"}); err != nil {
		log.Printf("evaluations: %d: mark running: %v", id, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	req := evaluateRequest{Repository: row.RepositoryFullName, PRNumber: row.PRNumber, CampaignID: row.CampaignID, Event: row.Event}
	if row.HeadSHA != nil {
		req.HeadSHA = *row.HeadSHA
	}
	res, err := e.runner.Evaluate(ctx, req)

	fields := map[string]any{"status": "failed"}
	if err != nil {
		fields["error"] = err.Error()
		log.Printf("evaluations: %d %s#%d failed: %v", id, row.RepositoryFullName, row.PRNumber, err)
	} else {
		fields = map[string]any{
			"status": "done", "score": res.Score, "eligible": res.Eligible, "reward": res.Reward,
			"evaluation_hash": res.EvaluationHash, "policy_hash": res.PolicyHash, "replayed": res.Replayed, "error": nil,
		}
		log.Printf("evaluations: %d %s#%d score=%d eligible=%t", id, row.RepositoryFullName, row.PRNumber, res.Score, res.Eligible)
	}
	if err := e.store.updateEvaluation(id, fields); err != nil {
		log.Printf("evaluations: %d: save result: %v", id, err)
	}
}

// prEvent is what an evaluation needs from a pull_request webhook.
type prEvent struct {
	Repo    string
	Number  int
	HeadSHA string
	Event   string // "opened" (preview) or "merged" (settlement)
}

// prEventFrom maps pull_request webhooks to evaluation events; ok=false means not evaluated.
func prEventFrom(event string, payload json.RawMessage) (prEvent, bool) {
	if event != "pull_request" {
		return prEvent{}, false
	}
	var p struct {
		Action      string `json:"action"`
		PullRequest struct {
			Number int  `json:"number"`
			Draft  bool `json:"draft"`
			Merged bool `json:"merged"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(payload, &p) != nil || p.PullRequest.Number <= 0 || p.Repository.FullName == "" {
		return prEvent{}, false
	}
	ev := prEvent{Repo: p.Repository.FullName, Number: p.PullRequest.Number, HeadSHA: p.PullRequest.Head.SHA}
	switch {
	case p.Action == "closed" && p.PullRequest.Merged:
		ev.Event = "merged"
	case p.PullRequest.Draft:
		return prEvent{}, false // Drafts are evaluated once ready for review.
	case p.Action == "opened" || p.Action == "reopened" || p.Action == "synchronize" || p.Action == "ready_for_review":
		ev.Event = "opened"
	default:
		return prEvent{}, false
	}
	return ev, true
}

// fromWebhook queues one evaluation per active campaign that includes the PR's repo.
func (e *evaluations) fromWebhook(ev *webhookEvent) {
	pr, ok := prEventFrom(ev.Event, ev.Payload)
	if !ok {
		return
	}
	campaigns, err := e.store.activeCampaignsForRepo(pr.Repo)
	if err != nil {
		log.Printf("evaluations: campaigns for %s: %v", pr.Repo, err)
		return
	}
	for _, id := range campaigns {
		row := evaluationRow{
			CampaignID: id, RepositoryFullName: pr.Repo, PRNumber: pr.Number, Event: pr.Event,
			Trigger: "webhook", HeadSHA: strPtr(pr.HeadSHA), DeliveryID: strPtr(ev.DeliveryID),
		}
		if evalID, created, err := e.enqueue(row); err != nil {
			log.Printf("evaluations: queue %s#%d for campaign %s: %v", pr.Repo, pr.Number, id, err)
		} else if created {
			log.Printf("evaluations: queued %d (%s#%d %s, campaign %s)", evalID, pr.Repo, pr.Number, pr.Event, id)
		}
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
