package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http"
	"github.com/smartcontractkit/cre-sdk-go/cre"
)

// apiCampaign is the part of cre-runner's GET /api/campaigns/{id} the policy uses.
type apiCampaign struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	RewardAsset    string          `json:"reward_asset"`
	MaxRewardPerPR json.Number     `json:"max_reward_per_pr"` // Kept as text: no float money math.
	MinScore       *int            `json:"min_score"`
	Eligibility    map[string]bool `json:"eligibility"`
	StartsAt       *string         `json:"starts_at"`
	EndsAt         *string         `json:"ends_at"`
	Repos          []string        `json:"repos"`
}

var assetDecimals = map[string]int{"USDC": 6, "SOL": 9}

type campaignInput struct {
	APIURL string
	ID     string
}

// fetchCampaign runs on each node. Returns JSON of the used fields so nodes reach identical consensus.
func fetchCampaign(in campaignInput, _ *slog.Logger, sr *http.SendRequester) (string, error) {
	path := "/api/campaigns/" + url.PathEscape(in.ID)
	res, err := sr.SendRequest(&http.Request{
		Url:          strings.TrimRight(in.APIURL, "/") + path,
		Method:       "GET",
		MultiHeaders: map[string]*http.HeaderValues{"Accept": {Values: []string{"application/json"}}},
	}).Await()
	if err != nil {
		return "", fmt.Errorf("campaign %s: %w", in.ID, err)
	}
	if res.StatusCode == 404 {
		return "", fmt.Errorf("campaign %s not found", in.ID)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("campaign %s -> HTTP %d", in.ID, res.StatusCode)
	}
	var body struct {
		Data apiCampaign `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return "", fmt.Errorf("campaign %s: %w", in.ID, err)
	}
	c := body.Data
	slices.Sort(c.Repos)
	out, err := json.Marshal(c)
	return string(out), err
}

// toPolicy checks the campaign applies to this PR now and maps it to the scoring policy.
func (c apiCampaign) toPolicy(repository string, now time.Time, w Weights) (CampaignPolicy, error) {
	if c.Status != "active" {
		return CampaignPolicy{}, fmt.Errorf("campaign %s is %s, not active", c.ID, c.Status)
	}
	if !slices.Contains(c.Repos, repository) {
		return CampaignPolicy{}, fmt.Errorf("repository %s is not in campaign %s", repository, c.ID)
	}
	if err := inWindow(now, c.StartsAt, c.EndsAt); err != nil {
		return CampaignPolicy{}, fmt.Errorf("campaign %s: %w", c.ID, err)
	}
	decimals, ok := assetDecimals[c.RewardAsset]
	if !ok {
		return CampaignPolicy{}, fmt.Errorf("campaign %s: unsupported reward asset %q", c.ID, c.RewardAsset)
	}
	minScore := 0
	if c.MinScore != nil {
		minScore = *c.MinScore
	}
	p := CampaignPolicy{
		ID:            c.ID,
		Asset:         c.RewardAsset,
		TokenDecimals: decimals,
		Eligibility: Eligibility{
			RequireMerged:      c.Eligibility["merged"],
			RequireCIPassed:    c.Eligibility["ci_passed"],
			RequireLinkedIssue: c.Eligibility["linked_issue"],
			MinScore:           minScore,
		},
		Weights: w,
		Reward:  RewardModel{Model: "score_based", Max: c.MaxRewardPerPR.String()},
	}
	if _, err := parseUnits(p.Reward.Max, decimals); err != nil {
		return CampaignPolicy{}, fmt.Errorf("campaign %s max_reward_per_pr: %w", c.ID, err)
	}
	return p, nil
}

func inWindow(now time.Time, startsAt, endsAt *string) error {
	parse := func(s *string) (time.Time, bool, error) {
		if s == nil || *s == "" {
			return time.Time{}, false, nil
		}
		t, err := time.Parse(time.RFC3339, *s)
		return t, err == nil, err
	}
	start, hasStart, err := parse(startsAt)
	if err != nil {
		return fmt.Errorf("bad starts_at: %w", err)
	}
	end, hasEnd, err := parse(endsAt)
	if err != nil {
		return fmt.Errorf("bad ends_at: %w", err)
	}
	switch {
	case hasStart && now.Before(start):
		return errors.New("has not started")
	case hasEnd && now.After(end):
		return errors.New("has ended")
	}
	return nil
}

// resolvePolicy loads the campaign from cre-runner, or uses the static config campaign offline.
func resolvePolicy(cfg *Config, runtime cre.Runtime, client *http.Client, req *EvaluationRequest) (CampaignPolicy, error) {
	if cfg.CampaignAPIURL == "" {
		if req.CampaignID != cfg.Campaign.ID {
			return CampaignPolicy{}, fmt.Errorf("unknown campaign %s", req.CampaignID)
		}
		p := *cfg.Campaign
		p.Weights = cfg.Weights
		return p, nil
	}
	raw, err := http.SendRequest(campaignInput{APIURL: cfg.CampaignAPIURL, ID: req.CampaignID},
		runtime, client, fetchCampaign, cre.ConsensusIdenticalAggregation[string]()).Await()
	if err != nil {
		return CampaignPolicy{}, err
	}
	var c apiCampaign
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return CampaignPolicy{}, err
	}
	return c.toPolicy(req.Repository, runtime.Now(), cfg.Weights) // DON time, same on every node.
}
