package main

import (
	"encoding/json"
	"fmt"
)

// reviewSlot is one configured reviewer with its consensus score and detail JSON.
type reviewSlot struct {
	Role   string
	Result ReviewerResult
	Detail string
}

// buildScorecard assembles the contributor-facing breakdown. It goes into evaluation_hash,
// so what the dashboard shows is exactly what was scored.
func buildScorecard(w Weights, e GitHubEvidence, gates []Gate, slots []reviewSlot) (*Scorecard, error) {
	card := &Scorecard{
		Weights:  w,
		Evidence: evidenceChecks(e),
		Reviews:  []ReviewCard{},
		Findings: []Finding{},
		Gates:    gates,
	}
	for _, s := range slots {
		rc := ReviewCard{Role: s.Role, Provider: s.Result.Provider, Score: s.Result.Score, Categories: []CategoryScore{}}
		if s.Detail != "" {
			var d reviewDetail
			if err := json.Unmarshal([]byte(s.Detail), &d); err != nil {
				return nil, fmt.Errorf("%s detail: %w", s.Role, err)
			}
			rc.Persona, rc.Categories = d.Persona, d.Categories
			card.Findings = append(card.Findings, d.Findings...)
		}
		card.Reviews = append(card.Reviews, rc)
	}
	return card, nil
}
