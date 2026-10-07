package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/cre-sdk-go/capabilities/blockchain/solana"
	"github.com/smartcontractkit/cre-sdk-go/cre"

	"contriboracle/contracts/solana/src/generated/contrib_oracle"
)

type RewardDecision struct {
	CampaignID     string // cre-runner campaign UUID = on-chain campaign id
	Repository     string
	PRNumber       int
	Contributor    string // GitHub login of the PR author.
	Recipient      string // Author's Solana wallet (Privy, pregenerated if they never signed in).
	Score          int
	Reward         *big.Int // token base units
	EvaluationHash string   // 0x-hex
	PolicyHash     string   // 0x-hex
}

// payoutComputeLimit bounds the forwarder transaction: forwarder checks plus on_report's
// PDA derivations, payout account creation and one token transfer. Must stay within the
// CRE Solana gas limit (300k by default, cre/simulation-limits.json ChainWrite.Solana.GasLimit).
const payoutComputeLimit = 200_000

// submitRewardDecision pays an eligible merged PR: it writes a RewardReport through the CRE
// forwarder to contrib_oracle's on_report, which checks the campaign and pays once per PR.
// Returns the transaction signature. Without Solana config the decision is only logged.
func submitRewardDecision(runtime cre.Runtime, cfg *SolanaConfig, d RewardDecision) (string, error) {
	logger := runtime.Logger()
	if cfg == nil {
		logger.Info("[solana stub] reward decision",
			"reward", d.Reward.String(), "contributor", d.Contributor, "recipient", d.Recipient,
			"repository", d.Repository, "pr", d.PRNumber, "evaluation_hash", d.EvaluationHash)
		return "", nil
	}

	program, report, accounts, err := buildRewardReport(cfg, d)
	if err != nil {
		return "", fmt.Errorf("reward report: %w", err)
	}
	contrib_oracle.ProgramID = program // Bindings default to the IDL address; follow the deployment.
	oracle, err := contrib_oracle.NewContribOracle(&solana.Client{ChainSelector: cfg.ChainSelector})
	if err != nil {
		return "", err
	}
	// The capability rejects a nil compute config (despite the bindings' comment).
	compute := &solana.ComputeConfig{ComputeLimit: payoutComputeLimit}
	reply, err := oracle.WriteReportFromRewardReport(runtime, report, accounts, compute).Await()
	if err != nil {
		return "", fmt.Errorf("solana payout: %w", err)
	}
	if reply.TxStatus != solana.TxStatus_TX_STATUS_SUCCESS ||
		(reply.ReceiverContractExecutionStatus != nil &&
			*reply.ReceiverContractExecutionStatus != solana.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS) {
		// The message can be a multi-line RPC error dump (with program logs): keep it on one line.
		msg := strings.Join(strings.Fields(reply.GetErrorMessage()), " ")
		return "", fmt.Errorf("solana payout failed (%s): %s", reply.TxStatus, msg)
	}
	sig := solanago.SignatureFromBytes(reply.TxSignature).String()
	logger.Info("reward paid on Solana", "tx", sig, "recipient", d.Recipient, "amount", d.Reward.String())
	return sig, nil
}

// buildRewardReport derives the report and the 11 accounts of the forwarder write, in the
// order the forwarder hashes them: forwarder state, forwarder authority, then on_report's
// own accounts (see solana/README.md "Accounts for the workflow write").
func buildRewardReport(cfg *SolanaConfig, d RewardDecision) (solanago.PublicKey, contrib_oracle.RewardReport, []*solana.AccountMeta, error) {
	var none contrib_oracle.RewardReport
	keys, err := parseKeys(map[string]string{
		"programId": cfg.ProgramID, "forwarderProgram": cfg.ForwarderProgram, "forwarderState": cfg.ForwarderState,
		"mint": cfg.Mint, "tokenProgram": cfg.TokenProgram, "recipient": d.Recipient,
	})
	if err != nil {
		return solanago.PublicKey{}, none, nil, err
	}
	program := keys["programId"]

	campaignID, err := uuidBytes(d.CampaignID)
	if err != nil {
		return program, none, nil, err
	}
	if d.Reward == nil || d.Reward.Sign() <= 0 || !d.Reward.IsUint64() {
		return program, none, nil, errors.New("reward must be a positive u64")
	}
	if d.Score < 0 || d.Score > 100 {
		return program, none, nil, errors.New("score must be 0-100")
	}
	evalHash, err := hash32(d.EvaluationHash)
	if err != nil {
		return program, none, nil, fmt.Errorf("evaluation_hash: %w", err)
	}
	policyHash, err := hash32(d.PolicyHash)
	if err != nil {
		return program, none, nil, fmt.Errorf("policy_hash: %w", err)
	}
	contribution := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", d.Repository, d.PRNumber)))

	pda := func(programID solanago.PublicKey, seeds ...[]byte) (solanago.PublicKey, error) {
		addr, _, err := solanago.FindProgramAddress(seeds, programID)
		return addr, err
	}
	campaign, err := pda(program, []byte("campaign"), campaignID[:])
	if err != nil {
		return program, none, nil, err
	}
	derived := map[string][][]byte{
		"vault":      {[]byte("vault"), campaign[:]},
		"config":     {[]byte("config")},
		"payout":     {[]byte("payout"), campaign[:], contribution[:]},
		"rent_payer": {[]byte("rent_payer")},
	}
	addrs := map[string]solanago.PublicKey{}
	for name, seeds := range derived {
		if addrs[name], err = pda(program, seeds...); err != nil {
			return program, none, nil, err
		}
	}
	fwdState := keys["forwarderState"]
	fwdAuthority, err := pda(keys["forwarderProgram"], []byte("forwarder"), fwdState[:], program[:])
	if err != nil {
		return program, none, nil, err
	}
	recipient, mint, tokenProgram := keys["recipient"], keys["mint"], keys["tokenProgram"]
	recipientToken, err := pda(solanago.SPLAssociatedTokenAccountProgramID, recipient[:], tokenProgram[:], mint[:])
	if err != nil {
		return program, none, nil, err
	}

	meta := func(k solanago.PublicKey, writable bool) *solana.AccountMeta {
		return &solana.AccountMeta{PublicKey: k.Bytes(), IsWritable: writable}
	}
	accounts := []*solana.AccountMeta{
		meta(fwdState, false),
		meta(fwdAuthority, false),
		meta(addrs["config"], false),
		meta(campaign, true),
		meta(addrs["vault"], true),
		meta(mint, false),
		meta(addrs["payout"], true),
		meta(recipientToken, true),
		meta(addrs["rent_payer"], true),
		meta(tokenProgram, false),
		meta(solanago.SystemProgramID, false),
	}
	report := contrib_oracle.RewardReport{
		CampaignId:     campaignID,
		ContributionId: contribution,
		Recipient:      recipient,
		Amount:         d.Reward.Uint64(),
		Score:          uint8(d.Score),
		EvaluationHash: evalHash,
		PolicyHash:     policyHash,
	}
	return program, report, accounts, nil
}

func parseKeys(in map[string]string) (map[string]solanago.PublicKey, error) {
	out := make(map[string]solanago.PublicKey, len(in))
	for name, v := range in {
		k, err := solanago.PublicKeyFromBase58(v)
		if err != nil {
			return nil, fmt.Errorf("%s %q is not a Solana address", name, v)
		}
		out[name] = k
	}
	return out, nil
}

// uuidBytes parses a campaign UUID into its 16 raw bytes (the on-chain campaign id).
func uuidBytes(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		return out, fmt.Errorf("campaign id %q is not a UUID", s)
	}
	copy(out[:], b)
	return out, nil
}

func hash32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("%q is not a 32-byte hex hash", s)
	}
	copy(out[:], b)
	return out, nil
}
