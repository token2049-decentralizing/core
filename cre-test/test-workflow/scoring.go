package main

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// evidenceScore is a deterministic 0-100 score from GitHub evidence only.
func evidenceScore(e GitHubEvidence) int {
	s := 0
	if len(e.LinkedIssues) > 0 {
		s += 25
	}
	if e.CIStatus == "PASS" {
		s += 25
	}
	if e.ReviewApprovals > 0 {
		s += 20
	}
	if e.TestsTouched {
		s += 20
	}
	// Huge diffs are harder to review; penalise them.
	switch size := e.Additions + e.Deletions; {
	case size <= 1000:
		s += 10
	case size <= 3000:
		s += 5
	}
	return s
}

func clamp(n int) int { return max(0, min(100, n)) }

// aggregateScore returns the weighted final score, rounded half up like JS Math.round.
func aggregateScore(evidence int, codeReviewer, llm ReviewerResult, w Weights) int {
	v := float64(clamp(evidence))*w.Evidence +
		float64(clamp(codeReviewer.Score))*w.CodeReviewer +
		float64(clamp(llm.Score))*w.LLM
	return int(math.Floor(v + 0.5))
}

func isEligible(e GitHubEvidence, score int, p CampaignPolicy) bool {
	el := p.Eligibility
	switch {
	case el.RequireMerged && !e.Merged,
		el.RequireCIPassed && e.CIStatus != "PASS",
		el.RequireLinkedIssue && len(e.LinkedIssues) == 0:
		return false
	}
	return score >= el.MinScore
}

// rewardAmount returns the reward in whole tokens (e.g. USDC).
func rewardAmount(score int, p CampaignPolicy) float64 {
	r := p.Reward
	switch r.Model {
	case "fixed":
		if score >= r.MinScore {
			return r.Amount
		}
	case "score_based":
		return r.Max * float64(score) / 100
	case "tiered":
		tiers := append([]Tier(nil), r.Tiers...)
		sort.Slice(tiers, func(i, j int) bool { return tiers[i].MinScore > tiers[j].MinScore })
		for _, t := range tiers {
			if score >= t.MinScore {
				return t.Amount
			}
		}
	}
	return 0
}

// toBaseUnits converts whole tokens to base units (rounded to token decimals).
func toBaseUnits(amount float64, decimals int) *big.Int {
	s := strconv.FormatFloat(amount, 'f', decimals, 64)
	n, _ := new(big.Int).SetString(strings.Replace(s, ".", "", 1), 10)
	return n
}
