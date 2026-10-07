package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/cre-sdk-go/capabilities/blockchain/solana"
	solanamock "github.com/smartcontractkit/cre-sdk-go/capabilities/blockchain/solana/mock"
	"github.com/smartcontractkit/cre-sdk-go/cre/testutils"
	"github.com/stretchr/testify/require"
)

const (
	devnetSelector   = 16423721717087811551
	testProgram      = "FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA"
	mockForwarder    = "7kuEAA3mSC1Tz8gQjnvH7bKFda9xSPRRin9SZbH49cNK"
	mockForwarderSt  = "5Tipz3yhTBdVsDbaBxZkrp7Gjf3brGq5SKkxReefPMP7"
	testMint         = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	testRecipient    = "7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV"
	testCampaignUUID = "11111111-2222-3333-4444-555555555555"
)

func testSolana() *SolanaConfig {
	return &SolanaConfig{ChainSelector: devnetSelector, ProgramID: testProgram, ForwarderProgram: mockForwarder,
		ForwarderState: mockForwarderSt, Mint: testMint, TokenProgram: solanago.TokenProgramID.String()}
}

func testDecision() RewardDecision {
	return RewardDecision{CampaignID: testCampaignUUID, Repository: "acme/pool", PRNumber: 102, Contributor: "alice",
		Recipient: testRecipient, Score: 97, Reward: big.NewInt(485000000),
		EvaluationHash: "0x" + strings.Repeat("ab", 32), PolicyHash: "0x" + strings.Repeat("cd", 32)}
}

func TestBuildRewardReport(t *testing.T) {
	program, r, accounts, err := buildRewardReport(testSolana(), testDecision())
	require.NoError(t, err)
	require.Equal(t, testProgram, program.String())

	require.Equal(t, [16]byte{0x11, 0x11, 0x11, 0x11, 0x22, 0x22, 0x33, 0x33, 0x44, 0x44, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}, r.CampaignId)
	require.Equal(t, sha256.Sum256([]byte("acme/pool#102")), r.ContributionId)
	require.Equal(t, testRecipient, r.Recipient.String())
	require.Equal(t, uint64(485000000), r.Amount)
	require.Equal(t, uint8(97), r.Score)
	require.Equal(t, byte(0xab), r.EvaluationHash[0])

	// Borsh layout on_report decodes: 16 + 32 + 32 + 8 + 1 + 32 + 32 bytes, no trailing data.
	b, err := r.Marshal()
	require.NoError(t, err)
	require.Len(t, b, 153)

	require.Len(t, accounts, 11)
	require.Equal(t, mockForwarderSt, solanago.PublicKeyFromBytes(accounts[0].PublicKey).String())
	writable := []bool{false, false, false, true, true, false, true, true, true, false, false}
	for i, a := range accounts {
		require.Equal(t, writable[i], a.IsWritable, "account %d", i)
	}
	require.Equal(t, testMint, solanago.PublicKeyFromBytes(accounts[5].PublicKey).String())
	require.Equal(t, solanago.TokenProgramID.Bytes(), accounts[9].PublicKey)
	require.Equal(t, solanago.SystemProgramID.Bytes(), accounts[10].PublicKey)
	// The recipient's associated token account for this mint.
	ata, _, err := solanago.FindAssociatedTokenAddress(r.Recipient, solanago.MustPublicKeyFromBase58(testMint))
	require.NoError(t, err)
	require.Equal(t, ata.Bytes(), accounts[7].PublicKey)
}

func TestBuildRewardReportRejectsBadInput(t *testing.T) {
	for name, mutate := range map[string]func(*RewardDecision, *SolanaConfig){
		"no recipient":      func(d *RewardDecision, _ *SolanaConfig) { d.Recipient = "" },
		"campaign not uuid": func(d *RewardDecision, _ *SolanaConfig) { d.CampaignID = "example-oss-2026" },
		"zero reward":       func(d *RewardDecision, _ *SolanaConfig) { d.Reward = big.NewInt(0) },
		"reward over u64":   func(d *RewardDecision, _ *SolanaConfig) { d.Reward = new(big.Int).Lsh(big.NewInt(1), 64) },
		"bad hash":          func(d *RewardDecision, _ *SolanaConfig) { d.PolicyHash = "0x12" },
		"bad mint":          func(_ *RewardDecision, c *SolanaConfig) { c.Mint = "nope" },
	} {
		d, c := testDecision(), testSolana()
		mutate(&d, c)
		_, _, _, err := buildRewardReport(c, d)
		require.Error(t, err, name)
	}
}

func solanaConfig() *Config {
	cfg := testConfig()
	cfg.Campaign.ID = testCampaignUUID
	cfg.Solana = testSolana()
	return cfg
}

func merged() map[string]any {
	req := request("merged")
	req["campaign_id"] = testCampaignUUID
	req["recipient_wallet"] = testRecipient
	return req
}

func mockSolana(t *testing.T, reply func(*solana.WriteReportRequest) (*solana.WriteReportReply, error)) {
	m, err := solanamock.NewClientCapability(devnetSelector, t)
	require.NoError(t, err)
	m.WriteReport = func(_ context.Context, in *solana.WriteReportRequest) (*solana.WriteReportReply, error) {
		return reply(in)
	}
}

func runWith(t *testing.T, cfg *Config, req map[string]any) (EvaluationResponse, *testutils.TestRuntime, error) {
	runtime := testutils.NewRuntime(t, testSecrets)
	out, err := onHTTPTrigger(cfg, runtime, payload(t, req))
	var res EvaluationResponse
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &res))
	}
	return res, runtime, err
}

func TestMergedPaysOnSolana(t *testing.T) {
	mockAPIs(t, 91)
	sig := solanago.Signature{1, 2, 3}
	var got *solana.WriteReportRequest
	mockSolana(t, func(in *solana.WriteReportRequest) (*solana.WriteReportReply, error) {
		got = in
		ok := solana.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		return &solana.WriteReportReply{TxStatus: solana.TxStatus_TX_STATUS_SUCCESS, ReceiverContractExecutionStatus: &ok, TxSignature: sig[:]}, nil
	})

	res, _, err := runWith(t, solanaConfig(), merged())
	require.NoError(t, err)
	require.True(t, res.Eligible)
	require.Equal(t, sig.String(), res.PayoutTx)
	require.Equal(t, solanago.MustPublicKeyFromBase58(testProgram).Bytes(), got.Receiver)
	require.Len(t, got.RemainingAccounts, 11)
	require.NotNil(t, got.Report)
}

func TestFailedPayoutFailsTheEvaluation(t *testing.T) {
	mockAPIs(t, 91)
	reverted := solana.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
	msg := "custom program error: 0x177b" // AlreadyPaid
	rpcDown := false
	mockSolana(t, func(*solana.WriteReportRequest) (*solana.WriteReportReply, error) {
		if rpcDown {
			return nil, errors.New("rpc unavailable")
		}
		return &solana.WriteReportReply{TxStatus: solana.TxStatus_TX_STATUS_SUCCESS, ReceiverContractExecutionStatus: &reverted, ErrorMessage: &msg}, nil
	})
	_, _, err := runWith(t, solanaConfig(), merged())
	require.ErrorContains(t, err, "0x177b")

	rpcDown = true
	_, _, err = runWith(t, solanaConfig(), merged())
	require.ErrorContains(t, err, "rpc unavailable")
}

func TestPreviewsAndUnconfiguredCampaignsDoNotWrite(t *testing.T) {
	mockAPIs(t, 91)
	mockSolana(t, func(*solana.WriteReportRequest) (*solana.WriteReportReply, error) {
		t.Fatal("no Solana write expected")
		return nil, nil
	})
	opened := merged()
	opened["event"] = "opened"
	res, _, err := runWith(t, solanaConfig(), opened)
	require.NoError(t, err)
	require.Empty(t, res.PayoutTx)

	cfg := solanaConfig()
	cfg.Solana = nil
	res, rt, err := runWith(t, cfg, merged())
	require.NoError(t, err)
	require.Empty(t, res.PayoutTx)
	require.True(t, logsContain(rt, "[solana stub]"))
}

func TestValidateSolanaConfig(t *testing.T) {
	cfg := solanaConfig()
	require.NoError(t, validateConfig(cfg))
	cfg.Solana.ChainSelector = 0
	require.ErrorContains(t, validateConfig(cfg), "chainSelector")
	cfg = solanaConfig()
	cfg.Solana.ProgramID = "x"
	require.ErrorContains(t, validateConfig(cfg), "programId")
}
