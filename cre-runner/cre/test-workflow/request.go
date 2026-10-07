package main

import (
	"encoding/json"
	"errors"
	"regexp"
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// Base58 Solana public key (32 bytes encode to 32-44 characters).
	solanaAddressRe = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
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
	case req.CampaignID == "":
		return nil, errors.New("campaign_id is required")
	case req.Event != "opened" && req.Event != "merged":
		return nil, errors.New("event must be 'opened' or 'merged'")
	case req.HeadSHA != "" && !shaRe.MatchString(req.HeadSHA):
		return nil, errors.New("head_sha must be a 40-char lowercase hex commit SHA")
	case req.RecipientWallet != "" && !solanaAddressRe.MatchString(req.RecipientWallet):
		return nil, errors.New("recipient_wallet must be a base58 Solana address")
	}
	// Go decodes a JSON null into a nil map; reject it like any non-object.
	if n, ok := raw["notes"]; ok && string(n) == "null" {
		return nil, errors.New("notes must be an object")
	}
	return &req, nil
}
