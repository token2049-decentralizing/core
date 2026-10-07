package main

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// evidenceChecks scores GitHub evidence check by check (deterministic, 0-100 in total).
func evidenceChecks(e GitHubEvidence) []EvidenceCheck {
	award := func(ok bool, pts int) int {
		if ok {
			return pts
		}
		return 0
	}
	issues := "no \"fixes #N\" in the PR description"
	if len(e.LinkedIssues) > 0 {
		refs := make([]string, len(e.LinkedIssues))
		for i, n := range e.LinkedIssues {
			refs[i] = fmt.Sprintf("#%d", n)
		}
		issues = strings.Join(refs, ", ")
	}
	ci := map[string]string{"PASS": "all checks passed", "FAIL": "a check failed"}[e.CIStatus]
	if ci == "" {
		ci = "no completed checks"
	}
	approvals := "no approving review"
	if e.ReviewApprovals > 0 {
		approvals = fmt.Sprintf("%d approving review(s)", e.ReviewApprovals)
	}
	tests := "no test files changed"
	if e.TestsTouched {
		tests = e.TestFile
	}
	// Huge diffs are harder to review; penalise them.
	size := e.Additions + e.Deletions
	sizePts := 0
	switch {
	case size <= 1000:
		sizePts = 10
	case size <= 3000:
		sizePts = 5
	}
	return []EvidenceCheck{
		{Check: "linked_issue", Points: award(len(e.LinkedIssues) > 0, 25), Max: 25, Detail: issues},
		{Check: "ci", Points: award(e.CIStatus == "PASS", 25), Max: 25, Detail: ci},
		{Check: "approval", Points: award(e.ReviewApprovals > 0, 20), Max: 20, Detail: approvals},
		{Check: "tests", Points: award(e.TestsTouched, 20), Max: 20, Detail: tests},
		{Check: "size", Points: sizePts, Max: 10, Detail: fmt.Sprintf("+%d / -%d lines", e.Additions, e.Deletions)},
	}
}

// evidenceScore is a deterministic 0-100 score from GitHub evidence only.
func evidenceScore(e GitHubEvidence) int {
	s := 0
	for _, c := range evidenceChecks(e) {
		s += c.Points
	}
	return s
}

func clamp(n int) int { return max(0, min(100, n)) }

// aggregateScore is the weighted score in integer math, rounded half up.
func aggregateScore(evidence int, codeReviewer, llm ReviewerResult, w Weights) int {
	sum := clamp(evidence)*w.Evidence + clamp(codeReviewer.Score)*w.CodeReviewer + clamp(llm.Score)*w.LLM
	return (sum + 5000) / 10000
}

// eligibilityGates lists every campaign gate; a PR is eligible when all required gates pass.
func eligibilityGates(e GitHubEvidence, score int, p CampaignPolicy) []Gate {
	el := p.Eligibility
	return []Gate{
		{Gate: "merged", Required: el.RequireMerged, Passed: e.Merged},
		{Gate: "ci_passed", Required: el.RequireCIPassed, Passed: e.CIStatus == "PASS"},
		{Gate: "linked_issue", Required: el.RequireLinkedIssue, Passed: len(e.LinkedIssues) > 0},
		{Gate: "min_score", Required: true, Passed: score >= el.MinScore, Detail: fmt.Sprintf("%d / %d", score, el.MinScore)},
	}
}

func isEligible(e GitHubEvidence, score int, p CampaignPolicy) bool {
	for _, g := range eligibilityGates(e, score, p) {
		if g.Required && !g.Passed {
			return false
		}
	}
	return true
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
