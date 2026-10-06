package main

import (
	"encoding/json"
	"errors"
	"regexp"
)

// Same contract as the workflow (test-workflow/types.go).
type evaluationRequest struct {
	Repository string         `json:"repository"`
	PRNumber   int            `json:"pr_number"`
	CampaignID string         `json:"campaign_id"`
	Event      string         `json:"event"`
	Notes      map[string]any `json:"notes,omitempty"`
}

type evaluationResponse struct {
	Score          int    `json:"score"`
	Eligible       bool   `json:"eligible"`
	Reward         string `json:"reward"`
	EvaluationHash string `json:"evaluation_hash"`
	PolicyHash     string `json:"policy_hash"`
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// parseRequest rejects bad input before spending a simulation on it.
func parseRequest(body []byte) (*evaluationRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("body must be a JSON object")
	}
	var req evaluationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, errors.New("invalid field types")
	}
	switch {
	case !repoRe.MatchString(req.Repository):
		return nil, errors.New("repository must be 'owner/repo'")
	case req.PRNumber <= 0:
		return nil, errors.New("pr_number must be a positive integer")
	case req.CampaignID == "":
		return nil, errors.New("campaign_id is required")
	case req.Event != "opened" && req.Event != "merged":
		return nil, errors.New("event must be 'opened' or 'merged'")
	}
	if n, ok := raw["notes"]; ok && string(n) == "null" {
		return nil, errors.New("notes must be an object")
	}
	return &req, nil
}
