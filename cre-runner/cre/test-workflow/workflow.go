package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http"
	"github.com/smartcontractkit/cre-sdk-go/cre"
)

func InitWorkflow(config *Config, _ *slog.Logger, _ cre.SecretsProvider) (cre.Workflow[*Config], error) {
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	keys := make([]*http.AuthorizedKey, 0, len(config.AuthorizedKeys))
	for _, k := range config.AuthorizedKeys {
		t, ok := http.KeyType_value[k.Type]
		if !ok {
			return nil, fmt.Errorf("unknown authorized key type %q", k.Type)
		}
		keys = append(keys, &http.AuthorizedKey{Type: http.KeyType(t), PublicKey: k.PublicKey})
	}
	return cre.Workflow[*Config]{
		cre.Handler(http.Trigger(&http.Config{AuthorizedKeys: keys}), onHTTPTrigger),
	}, nil
}

func validateConfig(c *Config) error {
	switch {
	case c.Mode != ModeSimulation && c.Mode != ModeProduction:
		return fmt.Errorf("mode must be %q or %q", ModeSimulation, ModeProduction)
	case c.GitHubAPIURL == "":
		return errors.New("githubApiUrl is required")
	case c.Campaign.ID == "":
		return errors.New("campaign.id is required")
	}
	w := c.Campaign.Weights
	if w.Evidence < 0 || w.CodeReviewer < 0 || w.LLM < 0 || w.Evidence+w.CodeReviewer+w.LLM != 10000 {
		return errors.New("campaign.weights must be non-negative basis points summing to 10000")
	}
	// Parse every amount now so a bad policy fails at startup, not at payout.
	for _, score := range []int{0, 50, 100} {
		if _, err := rewardBaseUnits(score, c.Campaign); err != nil {
			return fmt.Errorf("campaign.reward: %w", err)
		}
	}
	if c.Mode == ModeProduction {
		switch {
		case len(c.AuthorizedKeys) == 0:
			return errors.New("production needs authorizedKeys: deployed HTTP triggers only accept signed requests")
		case c.Reviewers.CodeReviewer.URL == "" || c.Reviewers.LLM.URL == "":
			return errors.New("production needs real reviewer URLs (stub scores are simulation-only)")
		}
	}
	return nil
}

func secret(runtime cre.Runtime, id string) (string, error) {
	s, err := runtime.GetSecret(&cre.SecretRequest{Id: id}).Await()
	if err != nil {
		return "", fmt.Errorf("secret %s: %w", id, err)
	}
	if s.Value == "" {
		return "", fmt.Errorf("secret %s is empty (check .env / secrets.yaml)", id)
	}
	return s.Value, nil
}

func reviewer(runtime cre.Runtime, cfg ReviewerConfig) (reviewerCall, error) {
	if cfg.URL == "" {
		return reviewerCall{}, nil
	}
	key, err := secret(runtime, cfg.SecretID)
	return reviewerCall{URL: cfg.URL, APIKey: key}, err
}

func providerName(name string, c reviewerCall) string {
	if c.URL == "" {
		return name + "-stub"
	}
	return name
}

func policyHash(p CampaignPolicy) (string, error) {
	if p.raw != nil {
		return hashJSON(p.raw)
	}
	return hashJSON(p)
}

// onHTTPTrigger returns the EvaluationResponse as a JSON string.
func onHTTPTrigger(cfg *Config, runtime cre.Runtime, payload *http.Payload) (string, error) {
	logger := runtime.Logger()
	req, err := parseRequest(payload.Input)
	if err != nil {
		return "", err
	}
	if req.CampaignID != cfg.Campaign.ID {
		return "", fmt.Errorf("unknown campaign %s", req.CampaignID)
	}
	logger.Info(fmt.Sprintf("evaluating %s#%d (%s)", req.Repository, req.PRNumber, req.Event))

	token, err := secret(runtime, "GITHUB_TOKEN")
	if err != nil {
		return "", err
	}
	client := &http.Client{}

	evidenceJSON, err := http.SendRequest(
		evidenceInput{APIURL: cfg.GitHubAPIURL, Token: token, Repository: req.Repository, PRNumber: req.PRNumber},
		runtime, client, fetchGitHubEvidence, cre.ConsensusIdenticalAggregation[string](),
	).Await()
	if err != nil {
		return "", err
	}
	var evidence GitHubEvidence
	if err := json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
		return "", err
	}
	if req.HeadSHA != "" && req.HeadSHA != evidence.HeadSHA {
		return "", fmt.Errorf("PR head moved: requested %s, current %s", req.HeadSHA, evidence.HeadSHA)
	}

	codeCall, err := reviewer(runtime, cfg.Reviewers.CodeReviewer)
	if err != nil {
		return "", err
	}
	llmCall, err := reviewer(runtime, cfg.Reviewers.LLM)
	if err != nil {
		return "", err
	}

	// No reviewer URLs: use the evidence score so local runs work without reviewer APIs.
	evScore := evidenceScore(evidence)
	scores := reviewScores{CodeReviewer: evScore, LLM: evScore}
	if codeCall.URL != "" || llmCall.URL != "" {
		scores, err = http.SendRequest(
			reviewInput{CodeReviewer: codeCall, LLM: llmCall, Evidence: evidence, StubScore: evScore},
			runtime, client, runReviews, cre.ConsensusAggregationFromTags[reviewScores](),
		).Await()
		if err != nil {
			return "", err
		}
	}
	codeReviewer := ReviewerResult{Provider: providerName("code-reviewer", codeCall), Score: scores.CodeReviewer}
	llm := ReviewerResult{Provider: providerName("llm", llmCall), Score: scores.LLM}

	score := aggregateScore(evScore, codeReviewer, llm, cfg.Campaign.Weights)
	gates := eligibilityGates(evidence, score, cfg.Campaign)
	eligible := isEligible(evidence, score, cfg.Campaign)
	card, err := buildScorecard(cfg.Campaign.Weights, evidence, gates, []reviewSlot{
		{Role: "code_reviewer", Result: codeReviewer, Detail: scores.CodeReviewerDetail},
		{Role: "llm", Result: llm, Detail: scores.LLMDetail},
	})
	if err != nil {
		return "", err
	}
	reward := big.NewInt(0)
	if eligible {
		if reward, err = rewardBaseUnits(score, cfg.Campaign); err != nil {
			return "", err
		}
	}

	pHash, err := policyHash(cfg.Campaign)
	if err != nil {
		return "", err
	}
	// notes are excluded on purpose: they must not change the evaluation.
	eHash, err := hashJSON(map[string]any{
		"repository":  req.Repository,
		"pr_number":   req.PRNumber,
		"campaign_id": req.CampaignID,
		"event":       req.Event,
		"evidence":    evidence,
		"reviewers":   []ReviewerResult{codeReviewer, llm},
		"score":       score,
		"eligible":    eligible,
		"reward":      reward.String(),
		"policy_hash": pHash,
		"scorecard":   card,
	})
	if err != nil {
		return "", err
	}

	// Only a merged, eligible PR moves money. "opened" is informational.
	if req.Event == "merged" && eligible && reward.Sign() > 0 {
		err := submitRewardDecision(runtime, RewardDecision{
			CampaignID:     req.CampaignID,
			Repository:     req.Repository,
			PRNumber:       req.PRNumber,
			Contributor:    evidence.Author,
			Score:          score,
			Reward:         reward,
			EvaluationHash: eHash,
			PolicyHash:     pHash,
		})
		if err != nil {
			return "", err
		}
	}

	out, err := json.Marshal(EvaluationResponse{
		Score:          score,
		Eligible:       eligible,
		Reward:         reward.String(),
		EvaluationHash: eHash,
		PolicyHash:     pHash,
		Scorecard:      card,
	})
	if err != nil {
		return "", err
	}
	logger.Info(fmt.Sprintf("result score=%d eligible=%t reward=%s evaluation_hash=%s", score, eligible, reward, eHash))
	return string(out), nil
}
