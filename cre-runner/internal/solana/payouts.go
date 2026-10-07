// Package solana prepares on-chain payouts for the CRE workflow: it reads the campaign's
// account in the contrib_oracle program (../../solana) and makes sure the recipient's token
// account exists, since the program pays into it but cannot create it.
package solana

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	ata "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/rpc"
)

// Devnet defaults: the CRE chain selector and Chainlink's mock forwarder, which
// `cre workflow simulate --broadcast` uses.
const (
	DevnetChainSelector   = 16423721717087811551
	DevnetRPC             = "https://api.devnet.solana.com"
	MockForwarderProgram  = "7kuEAA3mSC1Tz8gQjnvH7bKFda9xSPRRin9SZbH49cNK"
	MockForwarderState    = "5Tipz3yhTBdVsDbaBxZkrp7Gjf3brGq5SKkxReefPMP7"
	campaignStatusActive  = 0
	campaignAccountMinLen = 161
)

// RewardMints are the only reward tokens paid out, per campaign asset (Solana devnet):
// Circle's devnet USDC and wrapped SOL (the native mint, same address on every cluster).
var RewardMints = map[string]solanago.PublicKey{
	"USDC": solanago.MustPublicKeyFromBase58("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"),
	"SOL":  solanago.WrappedSol, // So111…112 (solanago.SolMint is So111…111, not the native mint)
}

// Anchor discriminator of the Campaign account (idl/contrib_oracle.json).
var campaignDiscriminator = []byte{50, 40, 49, 11, 157, 220, 229, 192}

// ErrCampaignNotOnChain: the campaign has not been created in the program yet.
var ErrCampaignNotOnChain = errors.New("campaign is not set up on Solana")

// Settings is what the workflow needs to pay a campaign's reward (its "solana" config).
type Settings struct {
	ChainSelector    uint64 `json:"chainSelector"`
	ProgramID        string `json:"programId"`
	ForwarderProgram string `json:"forwarderProgram"`
	ForwarderState   string `json:"forwarderState"`
	Mint             string `json:"mint"`
	TokenProgram     string `json:"tokenProgram"`
}

type Payouts struct {
	RPC              *rpc.Client
	Payer            solanago.PrivateKey // pays token account rent and fees (also CRE_SOLANA_PRIVATE_KEY)
	ChainSelector    uint64
	ProgramID        solanago.PublicKey
	ForwarderProgram solanago.PublicKey
	ForwarderState   solanago.PublicKey
	// ConfirmTimeout bounds waiting for the token account creation. Default 60s.
	ConfirmTimeout time.Duration
}

// ParsePrivateKey accepts what the cre CLI accepts for CRE_SOLANA_PRIVATE_KEY: a base58
// secret key or the path of a solana-keygen JSON file.
func ParsePrivateKey(v string) (solanago.PrivateKey, error) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, ".json") || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") {
		if rest, ok := strings.CutPrefix(v, "~/"); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			v = filepath.Join(home, rest)
		}
		return solanago.PrivateKeyFromSolanaKeygenFile(v)
	}
	k, err := solanago.PrivateKeyFromBase58(v)
	if err != nil || len(k) != 64 {
		return nil, errors.New("Solana private key must be base58 (64-byte secret) or a keypair file path")
	}
	return k, nil
}

func (p *Payouts) pda(seeds ...[]byte) (solanago.PublicKey, error) {
	addr, _, err := solanago.FindProgramAddress(seeds, p.ProgramID)
	return addr, err
}

// Prepare returns the workflow's Solana settings for a campaign and creates the
// recipient's token account for the campaign's mint if needed. The on-chain campaign must
// hold the devnet token of the campaign's reward asset (RewardMints).
func (p *Payouts) Prepare(ctx context.Context, campaignUUID, asset, recipient string) (*Settings, error) {
	want, ok := RewardMints[asset]
	if !ok {
		return nil, fmt.Errorf("reward asset %q has no Solana mint", asset)
	}
	id, err := uuidBytes(campaignUUID)
	if err != nil {
		return nil, err
	}
	owner, err := solanago.PublicKeyFromBase58(recipient)
	if err != nil {
		return nil, fmt.Errorf("recipient %q is not a Solana address", recipient)
	}
	campaign, err := p.pda([]byte("campaign"), id[:])
	if err != nil {
		return nil, err
	}

	data, _, err := p.account(ctx, campaign)
	if err != nil {
		return nil, fmt.Errorf("read campaign %s: %w", campaign, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%w (campaign account %s)", ErrCampaignNotOnChain, campaign)
	}
	mint, status, err := decodeCampaign(data)
	if err != nil {
		return nil, err
	}
	if status != campaignStatusActive {
		return nil, fmt.Errorf("campaign %s is not active on Solana", campaign)
	}
	if !mint.Equals(want) {
		return nil, fmt.Errorf("campaign %s holds mint %s, not devnet %s (%s)", campaign, mint, asset, want)
	}

	mintData, tokenProgram, err := p.account(ctx, mint)
	if err != nil {
		return nil, fmt.Errorf("read mint %s: %w", mint, err)
	}
	if mintData == nil {
		return nil, fmt.Errorf("mint %s does not exist", mint)
	}
	if !tokenProgram.Equals(solanago.TokenProgramID) && !tokenProgram.Equals(solanago.Token2022ProgramID) {
		return nil, fmt.Errorf("mint %s is not owned by a token program", mint)
	}
	if err := p.ensureTokenAccount(ctx, owner, mint, tokenProgram); err != nil {
		return nil, err
	}
	return &Settings{
		ChainSelector:    p.ChainSelector,
		ProgramID:        p.ProgramID.String(),
		ForwarderProgram: p.ForwarderProgram.String(),
		ForwarderState:   p.ForwarderState.String(),
		Mint:             mint.String(),
		TokenProgram:     tokenProgram.String(),
	}, nil
}

// account returns the account's data and owner, or nil data if it does not exist.
func (p *Payouts) account(ctx context.Context, key solanago.PublicKey) ([]byte, solanago.PublicKey, error) {
	res, err := p.RPC.GetAccountInfoWithOpts(ctx, key, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	if errors.Is(err, rpc.ErrNotFound) || (err == nil && (res == nil || res.Value == nil)) {
		return nil, solanago.PublicKey{}, nil
	}
	if err != nil {
		return nil, solanago.PublicKey{}, err
	}
	return res.Value.Data.GetBinary(), res.Value.Owner, nil
}

// decodeCampaign reads mint and status from a Campaign account:
// disc 8 | campaign_id 16 | sponsor 32 | mint 32 | vault 32 | max_reward_per_pr 8 | policy_hash 32 | status 1 | ...
func decodeCampaign(data []byte) (solanago.PublicKey, byte, error) {
	if len(data) < campaignAccountMinLen || !bytes.Equal(data[:8], campaignDiscriminator) {
		return solanago.PublicKey{}, 0, errors.New("account is not a contrib_oracle campaign")
	}
	return solanago.PublicKeyFromBytes(data[56:88]), data[160], nil
}

func (p *Payouts) ensureTokenAccount(ctx context.Context, owner, mint, tokenProgram solanago.PublicKey) error {
	addr, _, err := solanago.FindAssociatedTokenAddressWithProgram(owner, mint, tokenProgram)
	if err != nil {
		return err
	}
	data, _, err := p.account(ctx, addr)
	if err != nil {
		return fmt.Errorf("read token account %s: %w", addr, err)
	}
	if data != nil {
		return nil
	}

	ix, err := ata.NewCreateIdempotentInstructionWithTokenProgram(p.Payer.PublicKey(), owner, mint, tokenProgram).ValidateAndBuild()
	if err != nil {
		return err
	}
	block, err := p.RPC.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	if err != nil {
		return fmt.Errorf("latest blockhash: %w", err)
	}
	tx, err := solanago.NewTransaction([]solanago.Instruction{ix}, block.Value.Blockhash, solanago.TransactionPayer(p.Payer.PublicKey()))
	if err != nil {
		return err
	}
	if _, err := tx.Sign(func(k solanago.PublicKey) *solanago.PrivateKey {
		if k.Equals(p.Payer.PublicKey()) {
			return &p.Payer
		}
		return nil
	}); err != nil {
		return err
	}
	sig, err := p.RPC.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{PreflightCommitment: rpc.CommitmentConfirmed})
	if err != nil {
		return fmt.Errorf("create token account %s: %w", addr, err)
	}
	return p.confirm(ctx, sig)
}

func (p *Payouts) confirm(ctx context.Context, sig solanago.Signature) error {
	timeout := p.ConfirmTimeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		res, err := p.RPC.GetSignatureStatuses(ctx, true, sig)
		if err == nil && len(res.Value) == 1 && res.Value[0] != nil {
			st := res.Value[0]
			if st.Err != nil {
				return fmt.Errorf("transaction %s failed: %v", sig, st.Err)
			}
			if st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed || st.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("transaction %s not confirmed: %w", sig, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func uuidBytes(s string) ([16]byte, error) {
	var out [16]byte
	hexs := strings.ReplaceAll(s, "-", "")
	if len(hexs) != 32 {
		return out, fmt.Errorf("campaign id %q is not a UUID", s)
	}
	for i := 0; i < 16; i++ {
		var b byte
		if _, err := fmt.Sscanf(hexs[2*i:2*i+2], "%02x", &b); err != nil {
			return out, fmt.Errorf("campaign id %q is not a UUID", s)
		}
		out[i] = b
	}
	return out, nil
}
