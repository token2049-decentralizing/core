package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/token2049-decentralizing/core/cre-runner/internal/solana"
)

// campaignRow is the part of public.campaigns an execution needs.
type campaignRow struct {
	ID             string         `json:"id"`
	RewardAsset    string         `json:"reward_asset"`
	MaxRewardPerPR json.Number    `json:"max_reward_per_pr"`
	MinScore       *int           `json:"min_score"`
	Eligibility    map[string]any `json:"eligibility"` // free-form; reads {"merged", "ci_passed", "linked_issue"}
	StartsAt       *time.Time     `json:"starts_at"`
	EndsAt         *time.Time     `json:"ends_at"`
}

func (c campaignRow) runningAt(t time.Time) bool {
	return (c.StartsAt == nil || !t.Before(*c.StartsAt)) && (c.EndsAt == nil || t.Before(*c.EndsAt))
}

// workflowConfig mirrors the workflow's Config (cre/test-workflow/types.go).
type workflowConfig struct {
	Mode           string         `json:"mode"`
	AuthorizedKeys []any          `json:"authorizedKeys"`
	GitHubAPIURL   string         `json:"githubApiUrl"`
	Campaign       campaignPolicy `json:"campaign"`
	Reviewers      struct {
		CodeReviewer reviewerConfig `json:"codeReviewer"`
		LLM          reviewerConfig `json:"llm"`
	} `json:"reviewers"`
	// Set per merged execution when Solana payouts are enabled; nil = decision only logged.
	Solana *solana.Settings `json:"solana,omitempty"`
}

type campaignPolicy struct {
	ID            string `json:"id"`
	TokenDecimals int    `json:"tokenDecimals"`
	Eligibility   struct {
		RequireMerged      bool `json:"requireMerged"`
		RequireCIPassed    bool `json:"requireCiPassed"`
		RequireLinkedIssue bool `json:"requireLinkedIssue"`
		MinScore           int  `json:"minScore"`
	} `json:"eligibility"`
	Weights struct {
		Evidence     int `json:"evidenceBps"`
		CodeReviewer int `json:"codeReviewerBps"`
		LLM          int `json:"llmBps"`
	} `json:"weights"`
	Reward struct {
		Model string `json:"model"`
		Max   string `json:"max"`
	} `json:"reward"`
}

type reviewerConfig struct {
	URL      string `json:"url"` // Empty = stub score.
	SecretID string `json:"secretId"`
}

var tokenDecimals = map[string]int{"USDC": 6, "SOL": 9}

// buildWorkflowConfig turns a campaign into the workflow's simulation config.
// The reward scales with the score up to max_reward_per_pr. Campaign "scoring" is
// not mapped yet: the workflow keeps its default evidence/reviewer weights.
// reviewerURL is the base URL of the reviewer routes; empty stubs the reviewers.
func buildWorkflowConfig(c campaignRow, githubAPI, reviewerURL string) (*workflowConfig, error) {
	decimals, ok := tokenDecimals[c.RewardAsset]
	if !ok {
		return nil, fmt.Errorf("campaign %s: unsupported reward asset %q", c.ID, c.RewardAsset)
	}
	maxReward := trimDecimal(c.MaxRewardPerPR.String())
	if maxReward == "" {
		return nil, errors.New("campaign " + c.ID + ": max_reward_per_pr is missing")
	}

	cfg := &workflowConfig{Mode: "simulation", AuthorizedKeys: []any{}, GitHubAPIURL: githubAPI}
	p := &cfg.Campaign
	p.ID = c.ID
	p.TokenDecimals = decimals
	p.Eligibility.RequireMerged = c.Eligibility["merged"] == true
	p.Eligibility.RequireCIPassed = c.Eligibility["ci_passed"] == true
	p.Eligibility.RequireLinkedIssue = c.Eligibility["linked_issue"] == true
	if c.MinScore != nil {
		p.Eligibility.MinScore = *c.MinScore
	}
	p.Weights.Evidence, p.Weights.CodeReviewer, p.Weights.LLM = 4000, 3000, 3000
	p.Reward.Model, p.Reward.Max = "score_based", maxReward

	cfg.Reviewers.CodeReviewer.SecretID = "REVIEWER_TOKEN"
	cfg.Reviewers.LLM.SecretID = "REVIEWER_TOKEN"
	if reviewerURL != "" {
		cfg.Reviewers.CodeReviewer.URL = reviewerURL + "/review/code"
		cfg.Reviewers.LLM.URL = reviewerURL + "/review/issue"
	}
	return cfg, nil
}

// trimDecimal drops trailing fractional zeros from a numeric(20,6) value ("500.000000" -> "500"),
// so the policy hash does not depend on how Postgres pads the column.
func trimDecimal(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
