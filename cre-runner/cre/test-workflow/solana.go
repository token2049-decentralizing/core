package main

import (
	"math/big"

	"github.com/smartcontractkit/cre-sdk-go/cre"
)

type RewardDecision struct {
	CampaignID     string
	Repository     string
	PRNumber       int
	Contributor    string // GitHub login for now; map to a Solana address before settlement.
	Score          int
	Reward         *big.Int
	EvaluationHash string
	PolicyHash     string
}

// TODO: replace with a Solana write report once the program's on_report receiver exists.
// Steps: `cre generate-bindings solana` from the program IDL, then call the generated
// write-report helper here.
func submitRewardDecision(runtime cre.Runtime, d RewardDecision) error {
	runtime.Logger().Info("[solana stub] reward decision",
		"reward", d.Reward.String(), "contributor", d.Contributor,
		"repository", d.Repository, "pr", d.PRNumber, "evaluation_hash", d.EvaluationHash)
	return nil
}
