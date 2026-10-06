package main

import (
	"encoding/json"
	"errors"
	"regexp"
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// Campaign IDs are cre-runner UUIDs (or a slug for the static offline campaign).
	campaignRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// parseRequest validates the raw trigger payload.
func parseRequest(input []byte) (*EvaluationRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input, &raw); err != nil {
		return nil, errors.New("request must be a JSON object")
	}
	var req EvaluationRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, errors.New("request has invalid field types")
	}

	switch {
	case !repoRe.MatchString(req.Repository):
		return nil, errors.New("repository must be 'owner/repo'")
	case req.PRNumber <= 0:
		return nil, errors.New("pr_number must be a positive integer")
	case !campaignRe.MatchString(req.CampaignID):
		return nil, errors.New("campaign_id must be a campaign UUID")
	case req.Event != "opened" && req.Event != "merged":
		return nil, errors.New("event must be 'opened' or 'merged'")
	case req.HeadSHA != "" && !shaRe.MatchString(req.HeadSHA):
		return nil, errors.New("head_sha must be a 40-char lowercase hex commit SHA")
	}
	// Go decodes a JSON null into a nil map; reject it like any non-object.
	if n, ok := raw["notes"]; ok && string(n) == "null" {
		return nil, errors.New("notes must be an object")
	}
	return &req, nil
}
