package main

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
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

// aggregateScore is the weighted score in integer math, rounded half up.
func aggregateScore(evidence int, codeReviewer, llm ReviewerResult, w Weights) int {
	sum := clamp(evidence)*w.Evidence + clamp(codeReviewer.Score)*w.CodeReviewer + clamp(llm.Score)*w.LLM
	return (sum + 5000) / 10000
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

// parseUnits converts a decimal string of whole tokens to base units ("1.5", 6 -> 1500000).
func parseUnits(s string, decimals int) (*big.Int, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" || strings.Trim(whole+frac, "0123456789") != "" {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	if len(frac) > decimals {
		return nil, fmt.Errorf("amount %q has more than %d decimals", s, decimals)
	}
	n, ok := new(big.Int).SetString(whole+frac+strings.Repeat("0", decimals-len(frac)), 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	return n, nil
}

// rewardBaseUnits returns the reward in token base units.
func rewardBaseUnits(score int, p CampaignPolicy) (*big.Int, error) {
	r, dec := p.Reward, p.TokenDecimals
	switch r.Model {
	case "fixed":
		if score < r.MinScore {
			return big.NewInt(0), nil
		}
		return parseUnits(r.Amount, dec)
	case "score_based":
		maxUnits, err := parseUnits(r.Max, dec)
		if err != nil {
			return nil, err
		}
		n := new(big.Int).Mul(maxUnits, big.NewInt(int64(clamp(score))))
		return n.Quo(n, big.NewInt(100)), nil // Floor: never pay more than the policy allows.
	case "tiered":
		tiers := append([]Tier(nil), r.Tiers...)
		sort.Slice(tiers, func(i, j int) bool { return tiers[i].MinScore > tiers[j].MinScore })
		for _, t := range tiers {
			if score >= t.MinScore {
				return parseUnits(t.Amount, dec)
			}
		}
		return big.NewInt(0), nil
	}
	return nil, errors.New("unknown reward model " + r.Model)
}
