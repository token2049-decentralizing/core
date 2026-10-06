package main

import "encoding/json"

// Contract between the GitHub App and this workflow.

type EvaluationRequest struct {
	Repository string         `json:"repository"` // "owner/repo"
	PRNumber   int            `json:"pr_number"`
	CampaignID string         `json:"campaign_id"`
	Event      string         `json:"event"`              // "opened" | "merged"
	HeadSHA    string         `json:"head_sha,omitempty"` // Optional: fail if the PR head moved.
	Notes      map[string]any `json:"notes,omitempty"`    // Free-form, untrusted. Never affects score or reward.
}

type EvaluationResponse struct {
	Score          int    `json:"score"`
	Eligible       bool   `json:"eligible"`
	Reward         string `json:"reward"` // Token base units, as a string.
	EvaluationHash string `json:"evaluation_hash"`
	PolicyHash     string `json:"policy_hash"`
}

// Amounts are decimal strings in whole tokens ("500", "0.25"): no float math on money.
type Tier struct {
	MinScore int    `json:"minScore"`
	Amount   string `json:"amount"`
}

// Model is "fixed" (MinScore, Amount), "score_based" (Max) or "tiered" (Tiers).
type RewardModel struct {
	Model    string `json:"model"`
	MinScore int    `json:"minScore,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Max      string `json:"max,omitempty"`
	Tiers    []Tier `json:"tiers,omitempty"`
}

type Eligibility struct {
	RequireMerged      bool `json:"requireMerged"`
	RequireCIPassed    bool `json:"requireCiPassed"`
	RequireLinkedIssue bool `json:"requireLinkedIssue"`
	MinScore           int  `json:"minScore"`
}

// Weights in basis points; must sum to 10000.
type Weights struct {
	Evidence     int `json:"evidenceBps"`
	CodeReviewer int `json:"codeReviewerBps"`
	LLM          int `json:"llmBps"`
}

type CampaignPolicy struct {
	ID            string      `json:"id"`
	TokenDecimals int         `json:"tokenDecimals"`
	Eligibility   Eligibility `json:"eligibility"`
	Weights       Weights     `json:"weights"`
	Reward        RewardModel `json:"reward"`

	raw json.RawMessage // Exact config JSON, used for policy_hash.
}

func (p *CampaignPolicy) UnmarshalJSON(b []byte) error {
	type plain CampaignPolicy
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = append(json.RawMessage(nil), b...)
	return nil
}

type ReviewerConfig struct {
	URL      string `json:"url"` // Empty = stub score (for local testing).
	SecretID string `json:"secretId"`
}

type AuthorizedKey struct {
	Type      string `json:"type"` // "KEY_TYPE_ECDSA_EVM"
	PublicKey string `json:"publicKey"`
}

const (
	ModeSimulation = "simulation" // Local only: allows empty authorizedKeys and stub reviewers.
	ModeProduction = "production" // Deployable: requires authorizedKeys and real reviewers.
)

type Config struct {
	Mode           string          `json:"mode"`
	AuthorizedKeys []AuthorizedKey `json:"authorizedKeys"`
	GitHubAPIURL   string          `json:"githubApiUrl"`
	Campaign       CampaignPolicy  `json:"campaign"`
	Reviewers      struct {
		CodeReviewer ReviewerConfig `json:"codeReviewer"`
		LLM          ReviewerConfig `json:"llm"`
	} `json:"reviewers"`
}

type GitHubEvidence struct {
	Repository      string   `json:"repository"`
	PullRequest     int      `json:"pullRequest"`
	Author          string   `json:"author"`
	Title           string   `json:"title"`
	Body            string   `json:"body"`
	BaseSHA         string   `json:"baseSha"`
	HeadSHA         string   `json:"headSha"`
	Additions       int      `json:"additions"`
	Deletions       int      `json:"deletions"`
	ChangedFiles    int      `json:"changedFiles"`
	TestsTouched    bool     `json:"testsTouched"`
	LinkedIssues    []int    `json:"linkedIssues"`
	CIStatus        string   `json:"ciStatus"` // "PASS" | "FAIL" | "UNKNOWN"
	ReviewApprovals int      `json:"reviewApprovals"`
	Merged          bool     `json:"merged"`
	Labels          []string `json:"labels"`
}

type ReviewerResult struct {
	Provider string `json:"provider"`
	Score    int    `json:"score"` // 0-100
}
